package main

import (
	"bufio"
	"flag"
	"fmt"
	"github.com/masterzen/winrm/winrm"
	"github.com/mitchellh/packer/packer"
	"github.com/packer-community/winrmcp/winrmcp"
	"io/ioutil"
	"os"
	"strings"
	"time"
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

	cmd = winrm.Powershell(flag.Arg(0))
	client = winrm.NewClientWithParameters(&winrm.Endpoint{hostname, port}, user, pass, winrm.NewParameters(timeout, "en-US", 153600))
	var err error

	if !elevated {
		err = client.RunWithInput(cmd, os.Stdout, os.Stderr, os.Stdin)
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
	// Wrap the command in scheduled task
	tpl, err := packer.NewConfigTemplate()
	if err != nil {
		return err
	}

	// The command gets put into an interpolated string in the PS script,
	// so we need to escape any embedded quotes.
	escapedCmd := strings.Replace(cmd, "\"", "`\"", -1)

	elevatedScript, err := tpl.Process(ElevatedShellTemplate, &elevatedShellOptions{
		Command:  escapedCmd,
		User:     user,
		Password: pass,
	})
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
	writer := bufio.NewWriter(tmpFile)
	if _, err := writer.WriteString(elevatedScript); err != nil {
		return fmt.Errorf("Error preparing shell script: %s", err)
	}

	if err := writer.Flush(); err != nil {
		return fmt.Errorf("Error preparing shell script: %s", err)
	}

	tmpFile.Close()

	err = winrmcp.Copy(tmpFile.Name(), "$env:TEMP/packer-elevated-shell.ps1")

	if err != nil {
		return err
	}

	// Run the script that was uploaded
	command := fmt.Sprintf("powershell -executionpolicy bypass -file \"%s\"", "%TEMP%/packer-elevated-shell.ps1")
	err = client.RunWithInput(command, os.Stdout, os.Stderr, os.Stdin)
	return err
}

const ElevatedShellTemplate = `
$command = "{{.Command}}" + '; exit $LASTEXITCODE'
$user = '{{.User}}'
$password = '{{.Password}}'

$task_name = "packer-elevated-shell"
$out_file = "$env:TEMP\packer-elevated-shell.log"

if (Test-Path $out_file) {
  del $out_file
}

$task_xml = @'
<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <Principals>
    <Principal id="Author">
      <UserId>{user}</UserId>
      <LogonType>Password</LogonType>
      <RunLevel>HighestAvailable</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>false</StartWhenAvailable>
    <RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>
    <IdleSettings>
      <StopOnIdleEnd>true</StopOnIdleEnd>
      <RestartOnIdle>false</RestartOnIdle>
    </IdleSettings>
    <AllowStartOnDemand>true</AllowStartOnDemand>
    <Enabled>true</Enabled>
    <Hidden>false</Hidden>
    <RunOnlyIfIdle>false</RunOnlyIfIdle>
    <WakeToRun>false</WakeToRun>
    <ExecutionTimeLimit>PT2H</ExecutionTimeLimit>
    <Priority>4</Priority>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>cmd</Command>
      <Arguments>{arguments}</Arguments>
    </Exec>
  </Actions>
</Task>
'@

$bytes = [System.Text.Encoding]::Unicode.GetBytes($command)
$encoded_command = [Convert]::ToBase64String($bytes)
$arguments = "/c powershell.exe -EncodedCommand $encoded_command &gt; $out_file 2&gt;&amp;1"

$task_xml = $task_xml.Replace("{arguments}", $arguments)
$task_xml = $task_xml.Replace("{user}", $user)

$schedule = New-Object -ComObject "Schedule.Service"
$schedule.Connect()
$task = $schedule.NewTask($null)
$task.XmlText = $task_xml
$folder = $schedule.GetFolder("\")
$folder.RegisterTaskDefinition($task_name, $task, 6, $user, $password, 1, $null) | Out-Null

$registered_task = $folder.GetTask("\$task_name")
$registered_task.Run($null) | Out-Null

$timeout = 10
$sec = 0
while ( (!($registered_task.state -eq 4)) -and ($sec -lt $timeout) ) {
  Start-Sleep -s 1
  $sec++
}

function SlurpOutput($out_file, $cur_line) {
  if (Test-Path $out_file) {
    get-content $out_file | select -skip $cur_line | ForEach {
      $cur_line += 1
      Write-Host "$_" 
    }
  }
  return $cur_line
}

$cur_line = 0
do {
  Start-Sleep -m 100
  $cur_line = SlurpOutput $out_file $cur_line
} while (!($registered_task.state -eq 3))

$exit_code = $registered_task.LastTaskResult
[System.Runtime.Interopservices.Marshal]::ReleaseComObject($schedule) | Out-Null

exit $exit_code
`
