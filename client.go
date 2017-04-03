//Copyright (c) 2011-2013, Julien Laffaye <jlaffaye@FreeBSD.org>

//Permission to use, copy, modify, and/or distribute this software for any
//purpose with or without fee is hereby granted, provided that the above
//copyright notice and this permission notice appear in all copies.

//THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES
//WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF
//MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR
//ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
//WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN AN
//ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT OF
//OR IN CONNECTION WITH THE USE OR PERFORMANCE OF THIS SOFTWARE.

package ftp

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

// Dial is like DialTimeout with no timeout
func Dial(addr string) (*client, error) {
	return DialTimeout(addr, 0)
}

// DialTimeout initializes the connection to the specified ftp server address.
//
// It is generally followed by a call to Login() as most FTP commands require
// an authenticated user.
func DialTimeout(addr string, timeout time.Duration) (*client, error) {
	tconn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, err
	}
	// Use the resolved IP address in case addr contains a domain name
	// If we use the domain name, we might not resolve to the same IP.
	remoteAddr := tconn.RemoteAddr().String()
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return nil, err
	}

	conn := textproto.NewConn(tconn)

	c := &client{
		conn:     conn,
		host:     host,
		timeout:  timeout,
		features: make(map[string]string),
	}

	_, _, err = c.conn.ReadResponse(StatusReady)
	if err != nil {
		c.Close()
		return nil, err
	}

	err = c.feat()
	if err != nil {
		c.Close()
		return nil, err
	}

	if _, mlst := c.features["MLST"]; mlst {
		c.mlst = true
	}

	return c, nil
}

// Login authenticates the client with specified user and password.
//
// "anonymous"/"anonymous" is a common user/password scheme for FTP servers
// that allows anonymous read-only accounts.
func (c *client) Login(user, password string) error {
	code, message, err := c.cmd(-1, "USER %s", user)
	if err != nil {
		return err
	}

	switch code {
	case StatusLoggedIn:
	case StatusUserOK:
		_, _, err = c.cmd(StatusLoggedIn, "PASS %s", password)
		if err != nil {
			return err
		}
	default:
		return errors.New(message)
	}

	// Switch to binary mode
	if _, _, err = c.cmd(StatusCommandOK, "TYPE I"); err != nil {
		return err
	}

	// Switch to UTF-8
	if err := c.setUTF8(); err != nil {
		return err
	}

	return nil
}

// feat issues a FEAT FTP command to list the additional commands supported by
// the remote FTP server.
// FEAT is described in RFC 2389
func (c *client) feat() error {
	code, message, err := c.cmd(-1, "FEAT")
	if err != nil {
		return err
	}

	if code != StatusSystem {
		// The server does not support the FEAT command. This is not an
		// error: we consider that there is no additional feature.
		return nil
	}

	lines := strings.Split(message, "\n")
	for _, line := range lines {
		if !strings.HasPrefix(line, " ") {
			continue
		}

		line = strings.TrimSpace(line)
		featureElements := strings.SplitN(line, " ", 2)

		command := featureElements[0]

		var commandDesc string
		if len(featureElements) == 2 {
			commandDesc = featureElements[1]
		}

		c.features[command] = commandDesc
	}

	return nil
}

// setUTF8 issues an "OPTS UTF8 ON" command.
func (c *client) setUTF8() error {
	if _, ok := c.features["UTF8"]; !ok {
		return nil
	}

	code, message, err := c.cmd(-1, "OPTS UTF8 ON")
	if err != nil {
		return err
	}

	// The ftpd "filezilla-server" has FEAT support for UTF8, but always returns
	// "202 UTF8 mode is always enabled. No need to send this command." when
	// trying to use it. That's OK
	if code == StatusCommandNotImplemented {
		return nil
	}

	if code != StatusCommandOK {
		return errors.New(message)
	}

	return nil
}

// epsv issues an "EPSV" command to get a port number for a data connection.
func (c *client) epsv() (port int, err error) {
	_, line, err := c.cmd(StatusExtendedPassiveMode, "EPSV")
	if err != nil {
		return
	}

	start := strings.Index(line, "|||")
	end := strings.LastIndex(line, "|")
	if start == -1 || end == -1 {
		err = errors.New("Invalid EPSV response format")
		return
	}
	port, err = strconv.Atoi(line[start+3 : end])
	return
}

// pasv issues a "PASV" command to get a port number for a data connection.
func (c *client) pasv() (port int, err error) {
	_, line, err := c.cmd(StatusPassiveMode, "PASV")
	if err != nil {
		return
	}

	// PASV response format : 227 Entering Passive Mode (h1,h2,h3,h4,p1,p2).
	start := strings.Index(line, "(")
	end := strings.LastIndex(line, ")")
	if start == -1 || end == -1 {
		return 0, errors.New("Invalid PASV response format")
	}

	// We have to split the response string
	pasvData := strings.Split(line[start+1:end], ",")

	if len(pasvData) < 6 {
		return 0, errors.New("Invalid PASV response format")
	}

	// Let's compute the port number
	portPart1, err1 := strconv.Atoi(pasvData[4])
	if err1 != nil {
		err = err1
		return
	}

	portPart2, err2 := strconv.Atoi(pasvData[5])
	if err2 != nil {
		err = err2
		return
	}

	// Recompose port
	port = portPart1*256 + portPart2
	return
}

// getDataConnPort returns a port for a new data connection
// it uses the best available method to do so
func (c *client) getDataConnPort() (int, error) {
	if !c.unepsv {
		if port, err := c.epsv(); err == nil {
			return port, nil
		}
		// if there is an error, disable EPSV for the next attempts
		c.unepsv = true
	}
	return c.pasv()
}

// openDataConn creates a new FTP data connection.
func (c *client) openDataConn() (net.Conn, error) {
	port, err := c.getDataConnPort()
	if err != nil {
		return nil, err
	}

	return net.DialTimeout("tcp", net.JoinHostPort(c.host, strconv.Itoa(port)), c.timeout)
}

// cmd is a helper function to execute a command and check for the expected FTP
// return code
func (c *client) cmd(expected int, format string, args ...interface{}) (int, string, error) {
	_, err := c.conn.Cmd(format, args...)
	if err != nil {
		return 0, "", err
	}

	return c.conn.ReadResponse(expected)
}

// cmdDataConnFrom executes a command which require a FTP data connection.
// Issues a REST FTP command to specify the number of bytes to skip for the transfer.
func (c *client) cmdDataConnFrom(offset uint64, format string, args ...interface{}) (net.Conn, error) {
	conn, err := c.openDataConn()
	if err != nil {
		return nil, err
	}

	if offset != 0 {
		_, _, err := c.cmd(StatusRequestFilePending, "REST %d", offset)
		if err != nil {
			conn.Close()
			return nil, err
		}
	}

	_, err = c.conn.Cmd(format, args...)
	if err != nil {
		conn.Close()
		return nil, err
	}

	code, msg, err := c.conn.ReadResponse(-1)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if code != StatusAlreadyOpen && code != StatusAboutToSend {
		conn.Close()
		return nil, &textproto.Error{Code: code, Msg: msg}
	}

	return conn, nil
}

// NameList issues an NLST FTP command.
func (c *client) NameList(path string) (entries []string, err error) {
	conn, err := c.cmdDataConnFrom(0, "NLST %s", path)
	if err != nil {
		return
	}

	r := &response{conn, c}
	defer r.Close()

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		entries = append(entries, scanner.Text())
	}
	if err = scanner.Err(); err != nil {
		return entries, err
	}
	return
}

// List issues a LIST FTP command.
func (c *client) List(path string) (entries []*Entry, err error) {
	var cmd string
	var parseFunc func(string) (*Entry, error)

	if c.mlst {
		cmd = "MLSD"
		parseFunc = parseRFC3659ListLine
	} else {
		cmd = "LIST"
		parseFunc = parseListLine
	}

	conn, err := c.cmdDataConnFrom(0, "%s %s", cmd, path)
	if err != nil {
		return
	}

	r := &response{conn, c}
	defer r.Close()

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		entry, err := parseFunc(scanner.Text())
		if err == nil {
			entries = append(entries, entry)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return
}

// ChangeDir issues a CWD FTP command, which changes the current directory to
// the specified path.
func (c *client) ChangeDir(path string) error {
	_, _, err := c.cmd(StatusRequestedFileActionOK, "CWD %s", path)
	return err
}

// ChangeDirToParent issues a CDUP FTP command, which changes the current
// directory to the parent directory.  This is similar to a call to ChangeDir
// with a path set to "..".
func (c *client) ChangeDirToParent() error {
	_, _, err := c.cmd(StatusRequestedFileActionOK, "CDUP")
	return err
}

// CurrentDir issues a PWD FTP command, which Returns the path of the current
// directory.
func (c *client) CurrentDir() (string, error) {
	_, msg, err := c.cmd(StatusPathCreated, "PWD")
	if err != nil {
		return "", err
	}

	start := strings.Index(msg, "\"")
	end := strings.LastIndex(msg, "\"")

	if start == -1 || end == -1 {
		return "", errors.New("Unsuported PWD response format")
	}

	return msg[start+1 : end], nil
}

// FileSize issues a SIZE FTP command, which Returns the size of the file
func (c *client) FileSize(path string) (int64, error) {
	_, msg, err := c.cmd(StatusFile, "SIZE %s", path)
	if err != nil {
		return 0, err
	}

	return strconv.ParseInt(msg, 10, 64)
}

// Retr issues a RETR FTP command to fetch the specified file from the remote
// FTP server.
//
// The returned ReadCloser must be closed to cleanup the FTP data connection.
func (c *client) Retr(path string) (io.ReadCloser, error) {
	return c.RetrFrom(path, 0)
}

// RetrFrom issues a RETR FTP command to fetch the specified file from the remote
// FTP server, the server will not send the offset first bytes of the file.
//
// The returned ReadCloser must be closed to cleanup the FTP data connection.
func (c *client) RetrFrom(path string, offset uint64) (io.ReadCloser, error) {
	conn, err := c.cmdDataConnFrom(offset, "RETR %s", path)
	if err != nil {
		return nil, err
	}

	return &response{conn, c}, nil
}

// Stor issues a STOR FTP command to store a file to the remote FTP server.
// Stor creates the specified file with the content of the io.Reader.
//
// Hint: io.Pipe() can be used if an io.Writer is required.
func (c *client) Stor(path string, r io.Reader) error {
	return c.StorFrom(path, r, 0)
}

// StorFrom issues a STOR FTP command to store a file to the remote FTP server.
// Stor creates the specified file with the content of the io.Reader, writing
// on the server will start at the given file offset.
//
// Hint: io.Pipe() can be used if an io.Writer is required.
func (c *client) StorFrom(path string, r io.Reader, offset uint64) error {
	conn, err := c.cmdDataConnFrom(offset, "STOR %s", path)
	if err != nil {
		return err
	}

	_, err = io.Copy(conn, r)
	conn.Close()
	if err != nil {
		return err
	}

	_, _, err = c.conn.ReadResponse(StatusClosingDataConnection)
	return err
}

// Rename renames a file on the remote FTP server.
func (c *client) Rename(from, to string) error {
	_, _, err := c.cmd(StatusRequestFilePending, "RNFR %s", from)
	if err != nil {
		return err
	}

	_, _, err = c.cmd(StatusRequestedFileActionOK, "RNTO %s", to)
	return err
}

// MakeDir issues a MKD FTP command to create the specified directory on the
// remote FTP server.
func (c *client) MakeDir(path string) error {
	_, _, err := c.cmd(StatusPathCreated, "MKD %s", path)
	return err
}

// RemoveDir issues a RMD FTP command to remove the specified directory from
// the remote FTP server.
func (c *client) RemoveDir(path string) error {
	_, _, err := c.cmd(StatusRequestedFileActionOK, "RMD %s", path)
	return err
}

// NoOp issues a NOOP FTP command.
// NOOP has no effects and is usually used to prevent the remote FTP server to
// close the otherwise idle connection.
func (c *client) NoOp() error {
	_, _, err := c.cmd(StatusCommandOK, "NOOP")
	return err
}
