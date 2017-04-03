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
	"strconv"
	"strings"
	"time"
)

// EntryType describes the different types of an Entry.
type EntryType int

// The differents types of an Entry
const (
	EntryTypeFile EntryType = iota
	EntryTypeFolder
	EntryTypeLink
)

// Entry describes a file and is returned by List().
type Entry struct {
	Name string
	Type EntryType
	Size uint64
	Time time.Time
}

var (
	errUnsupportedListLine = errors.New("Unsupported LIST line")

	listLineParsers = []func(line string) (*Entry, error){
		parseRFC3659ListLine,
		parseLsListLine,
		parseDirListLine,
	}

	dirTimeFormats = []string{
		"01-02-06  03:04PM",
		"2006-01-02  15:04",
	}
)

// parseRFC3659ListLine parses the style of directory line defined in RFC 3659.
func parseRFC3659ListLine(line string) (*Entry, error) {
	iSemicolon := strings.Index(line, ";")
	iWhitespace := strings.Index(line, " ")

	if iSemicolon < 0 || iSemicolon > iWhitespace {
		return nil, errUnsupportedListLine
	}

	e := &Entry{
		Name: line[iWhitespace+1:],
	}

	for _, field := range strings.Split(line[:iWhitespace-1], ";") {
		i := strings.Index(field, "=")
		if i < 1 {
			return nil, errUnsupportedListLine
		}

		key := field[:i]
		value := field[i+1:]

		switch key {
		case "modify":
			var err error
			e.Time, err = time.Parse("20060102150405", value)
			if err != nil {
				return nil, err
			}
		case "type":
			switch value {
			case "dir", "cdir", "pdir":
				e.Type = EntryTypeFolder
			case "file":
				e.Type = EntryTypeFile
			}
		case "size":
			e.setSize(value)
		}
	}
	return e, nil
}

// parseLsListLine parses a directory line in a format based on the output of
// the UNIX ls command.
func parseLsListLine(line string) (*Entry, error) {
	fields := strings.Fields(line)
	if len(fields) >= 7 && fields[1] == "folder" && fields[2] == "0" {
		e := &Entry{
			Type: EntryTypeFolder,
			Name: strings.Join(fields[6:], " "),
		}
		if err := e.setTime(fields[3:6]); err != nil {
			return nil, err
		}

		return e, nil
	}

	if len(fields) < 8 {
		return nil, errUnsupportedListLine
	}

	if fields[1] == "0" {
		e := &Entry{
			Type: EntryTypeFile,
			Name: strings.Join(fields[7:], " "),
		}

		if err := e.setSize(fields[2]); err != nil {
			return nil, err
		}
		if err := e.setTime(fields[4:7]); err != nil {
			return nil, err
		}

		return e, nil
	}

	if len(fields) < 9 {
		return nil, errUnsupportedListLine
	}

	e := &Entry{}
	switch fields[0][0] {
	case '-':
		e.Type = EntryTypeFile
		if err := e.setSize(fields[4]); err != nil {
			return nil, err
		}
	case 'd':
		e.Type = EntryTypeFolder
	case 'l':
		e.Type = EntryTypeLink
	default:
		return nil, errors.New("Unknown entry type")
	}
	if err := e.setTime(fields[5:8]); err != nil {
		return nil, err
	}
	e.Name = strings.Join(fields[8:], " ")

	return e, nil
}

// parseDirListLine parses a directory line in a format based on the output of
// the MS-DOS DIR command.
func parseDirListLine(line string) (*Entry, error) {
	e := &Entry{}
	var err error

	// Try various time formats that DIR might use, and stop when one works.
	for _, format := range dirTimeFormats {
		if len(line) > len(format) {
			e.Time, err = time.Parse(format, line[:len(format)])
			if err == nil {
				line = line[len(format):]
				break
			}
		}
	}
	if err != nil {
		// None of the time formats worked.
		return nil, errUnsupportedListLine
	}

	line = strings.TrimLeft(line, " ")
	if strings.HasPrefix(line, "<DIR>") {
		e.Type = EntryTypeFolder
		line = strings.TrimPrefix(line, "<DIR>")
	} else {
		space := strings.Index(line, " ")
		if space == -1 {
			return nil, errUnsupportedListLine
		}
		e.Size, err = strconv.ParseUint(line[:space], 10, 64)
		if err != nil {
			return nil, errUnsupportedListLine
		}
		e.Type = EntryTypeFile
		line = line[space:]
	}

	e.Name = strings.TrimLeft(line, " ")
	return e, nil
}

// parseListLine parses the various non-standard format returned by the LIST
// FTP command.
func parseListLine(line string) (*Entry, error) {
	for _, f := range listLineParsers {
		e, err := f(line)
		if err != errUnsupportedListLine {
			return e, err
		}
	}
	return nil, errUnsupportedListLine
}

func (e *Entry) setSize(str string) (err error) {
	e.Size, err = strconv.ParseUint(str, 0, 64)
	return
}

func (e *Entry) setTime(fields []string) (err error) {
	var timeStr string
	if strings.Contains(fields[2], ":") { // this year
		thisYear, _, _ := time.Now().Date()
		timeStr = fields[1] + " " + fields[0] + " " + strconv.Itoa(thisYear)[2:4] + " " + fields[2] + " GMT"
	} else { // not this year
		if len(fields[2]) != 4 {
			return errors.New("Invalid year format in time string")
		}
		timeStr = fields[1] + " " + fields[0] + " " + fields[2][2:4] + " 00:00 GMT"
	}
	e.Time, err = time.Parse("_2 Jan 06 15:04 MST", timeStr)
	return
}
