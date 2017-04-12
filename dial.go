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
	"net/textproto"
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
	host, _, err := net.SplitHostPort(tconn.RemoteAddr().String())
	if err != nil {
		return nil, err
	}
	c := &client{
		host:     host,
		timeout:  timeout,
		conn:     textproto.NewConn(tconn),
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
	return c.setUTF8()
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
