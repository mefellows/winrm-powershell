package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strings"
	"time"

	"github.com/masterzen/winrm/winrm"
	"github.com/mitchellh/packer/common/uuid"
	"github.com/packer-community/winrmcp/winrmcp"
)

var (
	hostname string
	user     string
	pass     string
	cmd      string
	port     int
	elevated bool
	client   *winrm.Client
	timeout  string
)

func main() {

	flag.StringVar(&hostname, "hostname", "localhost", "winrm host")
	flag.StringVar(&user, "username", "vagrant", "winrm admin username")
	flag.StringVar(&pass, "password", "vagrant", "winrm admin password")
	flag.StringVar(&timeout, "timeout", "PT36000S", "winrm timeout")
	flag.IntVar(&port, "port", 5985, "winrm port")
	flag.BoolVar(&elevated, "elevated", false, "run as elevated user?")
	flag.Parse()

	cmd = flag.Arg(0)

	client, err := winrm.NewClientWithParameters(&winrm.Endpoint{Host: hostname, Port: port, HTTPS: false, Insecure: true, CACert: nil}, user, pass, winrm.NewParameters(timeout, "en-US", 153600))

	if !elevated {
		_, err = client.RunWithInput(winrm.Powershell(cmd), os.Stdout, os.Stderr, os.Stdin)
	} else {
		err = StartElevated()
	}

	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	os.Exit(0)
}

type elevatedShellOptions struct {
	Command  string
	User     string
	Password string
}

func StartElevated() (err error) {
	// The command gets put into an interpolated string in the PS script,
	// so we need to escape any embedded quotes.
	cmd = strings.Replace(cmd, "\"", "`\"", -1)

	elevatedScript, err := createCommandText()

	if err != nil {
		return err
	}

	// Upload the script which creates and manages the scheduled task
	winrmcp, err := winrmcp.New(fmt.Sprintf("%s:%d", hostname, port), &winrmcp.Config{
		Auth:                  winrmcp.Auth{user, pass},
		OperationTimeout:      time.Second * 60,
		MaxOperationsPerShell: 15,
	})
	tmpFile, err := ioutil.TempFile(os.TempDir(), "packer-elevated-shell.ps1")
	fmt.Printf("Temp file: %s", tmpFile.Name())
	writer := bufio.NewWriter(tmpFile)
	if _, err := writer.WriteString(elevatedScript); err != nil {
		return fmt.Errorf("Error preparing shell script: %s", err)
	}

	if err := writer.Flush(); err != nil {
		return fmt.Errorf("Error preparing shell script: %s", err)
	}

	tmpFile.Close()

	err = winrmcp.Copy(tmpFile.Name(), "${env:TEMP}/packer-elevated-shell.ps1")

	if err != nil {
		fmt.Printf("Error copying shell script: %s", err)
		return err
	}

	// Run the script that was uploaded
	command := fmt.Sprintf("powershell -executionpolicy bypass -file \"%s\"", "%TEMP%\\packer-elevated-shell.ps1")
	fmt.Printf("Running script: %s", command)
	client, err = winrm.NewClientWithParameters(&winrm.Endpoint{Host: hostname, Port: port, HTTPS: false, Insecure: true, CACert: nil}, user, pass, winrm.NewParameters(timeout, "en-US", 153600))
	_, err = client.RunWithInput(command, os.Stdout, os.Stderr, os.Stdin)
	return err
}

func createCommandText() (command string, err error) {

	// doesn't take well to the flattened env vars
	cmd = fmt.Sprintf(`$env:FOO="bar"; %s`, cmd)

	log.Printf("Building elevated command for: %s", cmd)

	// generate command
	var buffer bytes.Buffer
	err = elevatedTemplate.Execute(&buffer, elevatedOptions{
		User:            user,
		Password:        pass,
		TaskDescription: "Packer elevated task",
		TaskName:        fmt.Sprintf("packer-%s", uuid.TimeOrderedUUID()),
		EncodedCommand:  powershellEncode([]byte(cmd + "; exit $LASTEXITCODE")),
	})

	if err != nil {
		return "", err
	}

	log.Printf("ELEVATED SCRIPT: %s\n\n", string(buffer.Bytes()))
	return string(buffer.Bytes()), nil

}

func powershellEncode(buffer []byte) string {
	// 2 byte chars to make PowerShell happy
	wideCmd := ""
	for _, b := range buffer {
		wideCmd += string(b) + "\x00"
	}

	// Base64 encode the command
	input := []uint8(wideCmd)
	return base64.StdEncoding.EncodeToString(input)
}
