/*
Copyright (c) 2014 VMware, Inc. All Rights Reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package ipmi

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

type tool struct {
	*Connection

	passwdFile *os.File
}

func newToolTransport(c *Connection) transport {
	return &tool{Connection: c}
}

func (t *tool) open() error {
	var err error

	// create a temporary file to store the password
	t.passwdFile, err = os.CreateTemp("", "goipmi")
	if err != nil {
		return fmt.Errorf("error creating temporary file: %w", err)
	}

	if err = os.Remove(t.passwdFile.Name()); err != nil {
		t.passwdFile.Close() //nolint:errcheck

		return fmt.Errorf("error removing temporary file: %w", err)
	}

	if _, err = t.passwdFile.WriteString(t.Password); err != nil {
		t.passwdFile.Close() //nolint:errcheck

		return fmt.Errorf("error writing password: %w", err)
	}

	return nil
}

func (t *tool) close() error {
	return t.passwdFile.Close()
}

func (t *tool) send(req *Request, res Response) error {
	// ipmitool ... raw .. .. ..
	args := append([]string{"raw"}, requestToStrings(req)...)

	output, err := t.run(args...)
	if err != nil {
		// TODO: parse CompletionCode from stderr
		return err
	}

	return responseFromString(output, res)
}

func (t *tool) Console() error {
	cmd := t.cmd("sol", "activate", "-e", "&")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (t *tool) options() []string {
	intf := t.Interface
	if intf == "" {
		intf = "lanplus"
	}

	options := []string{
		"-H", t.Hostname,
		"-U", t.Username,
		"-f", "/proc/self/fd/3",
		"-I", intf,
	}

	if t.Port != 0 {
		options = append(options, "-p", strconv.Itoa(t.Port))
	}

	return options
}

func (t *tool) cmd(args ...string) *exec.Cmd {
	path := t.Path
	opts := append(t.options(), args...)

	if path == "" {
		path = "ipmitool"
	}

	cmd := exec.Command(path, opts...)
	cmd.ExtraFiles = []*os.File{
		t.passwdFile,
	}

	return cmd
}

func (t *tool) run(args ...string) (string, error) {
	cmd := t.cmd(args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// rewind the password file
	if _, err := t.passwdFile.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("error seeking the password file: %w", err)
	}

	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("run %s %s: %s (%s)",
			cmd.Path, strings.Join(cmd.Args, " "), stderr.String(), err)
	}

	return stdout.String(), err
}

func requestToBytes(r *Request) []byte {
	data := messageDataToBytes(r.Data)
	msg := make([]byte, 2+len(data))
	msg[0] = uint8(r.NetworkFunction)
	msg[1] = uint8(r.Command)
	copy(msg[2:], data)
	return msg
}

func requestToStrings(r *Request) []string {
	msg := requestToBytes(r)
	return rawEncode(msg)
}

func responseFromBytes(msg []byte, r Response) error {
	buf := make([]byte, 1+len(msg))
	buf[0] = uint8(CommandCompleted)
	copy(buf[1:], msg)
	return messageDataFromBytes(buf, r)
}

func responseFromString(s string, r Response) error {
	msg := rawDecode(strings.TrimSpace(s))
	return responseFromBytes(msg, r)
}

func rawDecode(data string) []byte {
	var buf bytes.Buffer

	for _, s := range strings.Split(data, " ") {
		b, err := hex.DecodeString(s)
		if err != nil {
			panic(err)
		}

		_, err = buf.Write(b)
		if err != nil {
			panic(err)
		}
	}

	return buf.Bytes()
}

func rawEncode(data []byte) []string {
	n := len(data)
	buf := make([]string, 0, n)

	// ipmitool needs every byte to be a separate argument
	for i := 0; i < n; i++ {
		buf = append(buf, "0x"+hex.EncodeToString(data[i:i+1]))
	}

	return buf
}
