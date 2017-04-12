//Copyright (c) 2011-2017, Julien Laffaye <jlaffaye@FreeBSD.org> and hwfy

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
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

type client struct {
	mlst     bool
	unepsv   bool
	host     string
	conn     *textproto.Conn
	timeout  time.Duration
	features map[string]string

	ftpSrv `json:"ftpSrvOptions"`
}

type ftpSrv struct {
	Addr string `json:"url"`
	User string `json:"user"`
	Pass string `json:"pwd"`
}

// Close issues a REIN FTP command to logout the current user and
// issues a QUIT FTP command to properly close the connection from
// the remote FTP server.
func (ftp *client) Close() (err error) {
	_, _, reinErr := ftp.cmd(StatusReady, "REIN")
	if reinErr != nil {
		err = reinErr
	}
	_, quitErr := ftp.conn.Cmd("QUIT")
	if quitErr != nil {
		err = quitErr
	}
	closeErr := ftp.conn.Close()
	if closeErr != nil {
		err = closeErr
	}
	return
}

// NewClient initialize ftp from the configuration file
func NewClient(path ...string) (*client, error) {
	cfg := "../config/system.config"

	if path != nil && path[0] != "" {
		cfg = path[0]
	}
	bytes, err := ioutil.ReadFile(cfg)
	if err != nil {
		return nil, errors.New("Read configuration file failed, " + err.Error())
	}
	ftp := new(client)
	if err = json.Unmarshal(bytes, ftp); err != nil {
		return nil, err
	}
	//the tcp connection address must be added to port 21
	if !strings.HasSuffix(ftp.Addr, ":21") {
		ftp.Addr += ":21"
	}
	conn, err := Dial(ftp.Addr)
	if err != nil {
		return nil, fmt.Errorf("Connection FTP failed,%s", err)
	}
	err = conn.Login(ftp.User, ftp.Pass)
	if err != nil {
		return nil, fmt.Errorf("Login FTP failed,%s", err)
	}
	return conn, nil
}

// Delete delete the matching files in the specified directory
func (ftp *client) Delete(dirName, fileName string) error {
	conn, err := ftp.cmdDataConnFrom(0, "NLST %s", dirName)
	if err != nil {
		return err
	}
	rep := &response{conn, ftp}
	defer rep.Close()

	dirEmpty := true
	scanner := bufio.NewScanner(rep)

	for scanner.Scan() {
		if strings.Contains(scanner.Text(), fileName) {
			ftp.Remove(scanner.Text())
		}
		dirEmpty = false
	}
	if dirEmpty {
		ftp.RemoveDir(dirName)
	}
	return scanner.Err()
}

// Upload upload files
func (ftp *client) Upload(dirName, fileName string, buf []byte) error {
	//the directory name can not start with "/"
	ftp.MakeDir(dirName)
	//select the current ftp directory
	ftp.ChangeDir(dirName)

	localFile := bytes.NewReader(buf)

	err := ftp.Stor(fileName, localFile)
	if err != nil {
		return err
	}
	//return to the root directory
	return ftp.ChangeDir("../../../")
}

// Names if the current directory exists to return a map
// key is the subdirectory name, value is subdirectory under all file names
func (ftp *client) Names(dirName string) (map[string][]string, error) {
	dir := make(map[string][]string)
	//get the file list
	list, err := ftp.List(dirName)
	if err != nil || list == nil {
		return dir, fmt.Errorf("The directory does not exist or is empty: %s %s", err, dirName)
	}
	ftp.ChangeDir(dirName)

	for _, file := range list {
		//the file type is 1 for the directory
		if file.Type == 1 {
			var prefixs []string
			//get all the file names in the directory
			names, _ := ftp.NameList(file.Name)
			//remove the suffix name of the file one by one
			for _, name := range names {
				prefix := strings.SplitN(name, ".", 2)
				prefixs = append(prefixs, prefix[0])
			}
			dir[file.Name] = prefixs
		}
	}
	return dir, nil
}

// NameList issues an NLST FTP command.
func (ftp *client) NameList(path string) (entries []string, err error) {
	conn, err := ftp.cmdDataConnFrom(0, "NLST %s", path)
	if err != nil {
		return
	}
	r := &response{conn, ftp}
	defer r.Close()

	scanner := bufio.NewScanner(r)

	for scanner.Scan() {
		entries = append(entries, scanner.Text())
	}
	return entries, scanner.Err()
}

// List issues a LIST FTP command.
func (ftp *client) List(path string) (entries []*Entry, err error) {
	var cmd string
	var parseFunc func(string) (*Entry, error)

	if ftp.mlst {
		cmd = "MLSD"
		parseFunc = parseRFC3659ListLine
	} else {
		cmd = "LIST"
		parseFunc = parseListLine
	}
	conn, err := ftp.cmdDataConnFrom(0, "%s %s", cmd, path)
	if err != nil {
		return
	}
	r := &response{conn, ftp}
	defer r.Close()

	scanner := bufio.NewScanner(r)

	for scanner.Scan() {
		entry, err := parseFunc(scanner.Text())
		if err == nil {
			entries = append(entries, entry)
		}
	}
	return entries, scanner.Err()
}

// ChangeDir issues a CWD FTP command, which changes the current directory to
// the specified path.
func (ftp *client) ChangeDir(path string) error {
	_, _, err := ftp.cmd(StatusRequestedFileActionOK, "CWD %s", path)
	return err
}

// ChangeDirToParent issues a CDUP FTP command, which changes the current
// directory to the parent directory.  This is similar to a call to ChangeDir
// with a path set to "..".
func (ftp *client) ChangeDirToParent() error {
	_, _, err := ftp.cmd(StatusRequestedFileActionOK, "CDUP")
	return err
}

// CurrentDir issues a PWD FTP command, which Returns the path of the current
// directory.
func (ftp *client) CurrentDir() (string, error) {
	_, msg, err := ftp.cmd(StatusPathCreated, "PWD")
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
func (ftp *client) FileSize(path string) (int64, error) {
	_, msg, err := ftp.cmd(StatusFile, "SIZE %s", path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(msg, 10, 64)
}

// Retr issues a RETR FTP command to fetch the specified file from the remote
// FTP server.
//
// The returned ReadCloser must be closed to cleanup the FTP data connection.
func (ftp *client) Retr(path string) (io.ReadCloser, error) {
	return ftp.RetrFrom(path, 0)
}

// RetrFrom issues a RETR FTP command to fetch the specified file from the remote
// FTP server, the server will not send the offset first bytes of the file.
//
// The returned ReadCloser must be closed to cleanup the FTP data connection.
func (ftp *client) RetrFrom(path string, offset uint64) (io.ReadCloser, error) {
	conn, err := ftp.cmdDataConnFrom(offset, "RETR %s", path)
	if err != nil {
		return nil, err
	}
	return &response{conn, ftp}, nil
}

// Stor issues a STOR FTP command to store a file to the remote FTP server.
// Stor creates the specified file with the content of the io.Reader.
//
// Hint: io.Pipe() can be used if an io.Writer is required.
func (ftp *client) Stor(path string, r io.Reader) error {
	return ftp.StorFrom(path, r, 0)
}

// StorFrom issues a STOR FTP command to store a file to the remote FTP server.
// Stor creates the specified file with the content of the io.Reader, writing
// on the server will start at the given file offset.
//
// Hint: io.Pipe() can be used if an io.Writer is required.
func (ftp *client) StorFrom(path string, r io.Reader, offset uint64) error {
	conn, err := ftp.cmdDataConnFrom(offset, "STOR %s", path)
	if err != nil {
		return err
	}
	_, err = io.Copy(conn, r)
	conn.Close()
	if err != nil {
		return err
	}
	_, _, err = ftp.conn.ReadResponse(StatusClosingDataConnection)
	return err
}

// Rename renames a file on the remote FTP server.
func (ftp *client) Rename(from, to string) error {
	_, _, err := ftp.cmd(StatusRequestFilePending, "RNFR %s", from)
	if err != nil {
		return err
	}

	_, _, err = ftp.cmd(StatusRequestedFileActionOK, "RNTO %s", to)
	return err
}

// Remove issues a DELE FTP command to delete the specified file from the
// remote FTP server.
func (ftp *client) Remove(path string) error {
	_, _, err := ftp.cmd(StatusRequestedFileActionOK, "DELE %s", path)
	return err
}

// MakeDir issues a MKD FTP command to create the specified directory on the
// remote FTP server.
func (ftp *client) MakeDir(path string) error {
	_, _, err := ftp.cmd(StatusPathCreated, "MKD %s", path)
	return err
}

// RemoveDir issues a RMD FTP command to remove the specified directory from
// the remote FTP server.
func (ftp *client) RemoveDir(path string) error {
	_, _, err := ftp.cmd(StatusRequestedFileActionOK, "RMD %s", path)
	return err
}

// NoOp issues a NOOP FTP command.
// NOOP has no effects and is usually used to prevent the remote FTP server to
// close the otherwise idle connection.
func (ftp *client) NoOp() error {
	_, _, err := ftp.cmd(StatusCommandOK, "NOOP")
	return err
}
