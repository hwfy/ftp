package ftp

import (
	"net"
)

// response represent a data-connection
type response struct {
	conn net.Conn
	c    *client
}

// Read implements the io.Reader interface on a FTP data connection.
func (r *response) Read(buf []byte) (int, error) {
	return r.conn.Read(buf)
}

// Close implements the io.Closer interface on a FTP data connection.
func (r *response) Close() error {
	err := r.conn.Close()
	_, _, err2 := r.c.conn.ReadResponse(StatusClosingDataConnection)
	if err2 != nil {
		err = err2
	}
	return err
}
