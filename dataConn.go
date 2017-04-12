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
	"errors"
	"net"
	"strconv"
	"strings"
)

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
