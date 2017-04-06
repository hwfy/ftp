// ftpClient
package ftp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/textproto"
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
	//get the file list
	list, err := ftp.List(dirName)
	if err != nil || list == nil {
		return nil, fmt.Errorf("The directory does not exist or is empty: %s %s", err, dirName)
	}
	ftp.ChangeDir(dirName)

	dir := make(map[string][]string)

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
