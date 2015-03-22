# WinRM Powershell
Run Powershell remotely from the CLI

A tiny wrapper around masterzen/winrm to execute Powershell commands remotely over
WinRM from any OS, including elevated user support.

## Usage

```
winrm-powershell --help
Usage of bin/winrm-powershell:
  -elevated=false: run as elevated user?
  -hostname="localhost": winrm host
  -password="vagrant": winrm admin password
  -port=5985: winrm port
  -timeout="PT36000S": winrm timeout
  -username="vagrant": winrm admin username
```

Example basic command:
```
winrm-powershell whoami
vagrant2012r2\vagrant
```
