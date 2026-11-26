package main

import (
	"crypto/rand"
	_ "embed"
	"encoding/xml"
	"fmt"
	"math/big"
	"strings"
)

//go:embed winprovision.ps1
var provisionScript string

// provisionName is where the answer file finds the provisioning script: the
// root of the boot disk, whichever drive letter Windows gives that disk.
const provisionName = "osvm-provision.ps1"

// labConfigBypass are the Windows 11 Setup hardware checks a CI builder does
// not need to pass: Setup itself is told so in WinPE, before it looks.
var labConfigBypass = []string{"BypassTPMCheck", "BypassSecureBootCheck", "BypassRAMCheck", "BypassCPUCheck", "BypassStorageCheck"}

// buildUser is the local administrator the build logs on as. On a client SKU
// the built-in Administrator is disabled, so (as in cloudbase's
// UnattendTemplate.xml, "needed on a client OS") the answer file creates a
// LocalAccount for the AutoLogon; provisioning deactivates it before sysprep.
const buildUser = "osvmbuild"

// genericProKey is Microsoft's published generic installation key for Windows
// 11 Pro. It only selects the edition and skips Setup's product-key page: it
// does not activate, and the system stays unactivated.
const genericProKeyValue = "VK7JG-NPHTM-C97JM-9MPGT-3V66T"

func genericProKey(edition string) string {
	if edition == "Windows 11 Pro" {
		return genericProKeyValue
	}
	return ""
}

func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// randomPassword returns n characters from an alphabet that needs no XML or
// shell quoting. The builder's Administrator password lives only in the answer
// file on a disk that is deleted after the build, and sysprep'd images get a
// fresh one from cloudbase-init.
func randomPassword(n int) (string, error) {
	const alphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, n)
	for i := range b {
		k, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", err
		}
		b[i] = alphabet[k.Int64()]
	}
	return string(b), nil
}

// unattendXML is the answer file: it bypasses the Windows 11 hardware checks,
// partitions disk 0 (GPT: ESP, MSR, Windows), installs the named edition with
// no product key (the system stays unactivated), skips OOBE and its network
// requirement, logs Administrator on once and runs the provisioning script.
func unattendXML(edition, password string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?>
<unattend xmlns="urn:schemas-microsoft-com:unattend" xmlns:wcm="http://schemas.microsoft.com/WMIConfig/2002/State">
  <settings pass="windowsPE">
    <component name="Microsoft-Windows-International-Core-WinPE" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
      <SetupUILanguage><UILanguage>en-US</UILanguage></SetupUILanguage>
      <InputLocale>en-US</InputLocale><SystemLocale>en-US</SystemLocale><UILanguage>en-US</UILanguage><UserLocale>en-US</UserLocale>
    </component>
    <component name="Microsoft-Windows-Setup" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
      <RunSynchronous>
`)
	for i, k := range labConfigBypass {
		fmt.Fprintf(&b, "        <RunSynchronousCommand wcm:action=\"add\"><Order>%d</Order><Path>reg add HKLM\\SYSTEM\\Setup\\LabConfig /v %s /t REG_DWORD /d 1 /f</Path></RunSynchronousCommand>\n", i+1, k)
	}
	b.WriteString(`      </RunSynchronous>
      <DiskConfiguration>
        <Disk wcm:action="add">
          <DiskID>0</DiskID>
          <WillWipeDisk>true</WillWipeDisk>
          <CreatePartitions>
            <CreatePartition wcm:action="add"><Order>1</Order><Type>EFI</Type><Size>100</Size></CreatePartition>
            <CreatePartition wcm:action="add"><Order>2</Order><Type>MSR</Type><Size>16</Size></CreatePartition>
            <CreatePartition wcm:action="add"><Order>3</Order><Type>Primary</Type><Extend>true</Extend></CreatePartition>
          </CreatePartitions>
          <ModifyPartitions>
            <ModifyPartition wcm:action="add"><Order>1</Order><PartitionID>1</PartitionID><Format>FAT32</Format><Label>System</Label></ModifyPartition>
            <ModifyPartition wcm:action="add"><Order>2</Order><PartitionID>2</PartitionID></ModifyPartition>
            <ModifyPartition wcm:action="add"><Order>3</Order><PartitionID>3</PartitionID><Format>NTFS</Format><Label>Windows</Label><Letter>C</Letter></ModifyPartition>
          </ModifyPartitions>
        </Disk>
      </DiskConfiguration>
      <ImageInstall>
        <OSImage>
          <InstallFrom><MetaData wcm:action="add"><Key>/IMAGE/NAME</Key><Value>`)
	b.WriteString(xmlEscape(edition))
	b.WriteString(`</Value></MetaData></InstallFrom>
          <InstallTo><DiskID>0</DiskID><PartitionID>3</PartitionID></InstallTo>
        </OSImage>
      </ImageInstall>
      <UserData>
        <AcceptEula>true</AcceptEula>
        <ProductKey><Key>` + genericProKey(edition) + `</Key><WillShowUI>Never</WillShowUI></ProductKey>
      </UserData>
    </component>
  </settings>
  <settings pass="specialize">
    <component name="Microsoft-Windows-Deployment" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
      <RunSynchronous>
        <RunSynchronousCommand wcm:action="add"><Order>1</Order><Path>reg add HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\OOBE /v BypassNRO /t REG_DWORD /d 1 /f</Path></RunSynchronousCommand>
      </RunSynchronous>
    </component>
  </settings>
  <settings pass="oobeSystem">
    <component name="Microsoft-Windows-Shell-Setup" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
      <OOBE>
        <HideEULAPage>true</HideEULAPage>
        <HideLocalAccountScreen>true</HideLocalAccountScreen>
        <HideOnlineAccountScreens>true</HideOnlineAccountScreens>
        <HideWirelessSetupInOOBE>true</HideWirelessSetupInOOBE>
        <ProtectYourPC>3</ProtectYourPC>
        <SkipMachineOOBE>true</SkipMachineOOBE>
        <SkipUserOOBE>true</SkipUserOOBE>
      </OOBE>
      <TimeZone>UTC</TimeZone>
      <UserAccounts>
        <AdministratorPassword><Value>`)
	b.WriteString(xmlEscape(password))
	b.WriteString(`</Value><PlainText>true</PlainText></AdministratorPassword>
        <LocalAccounts>
          <LocalAccount wcm:action="add">
            <Password><Value>`)
	b.WriteString(xmlEscape(password))
	b.WriteString(`</Value><PlainText>true</PlainText></Password>
            <Name>` + buildUser + `</Name>
            <Group>Administrators</Group>
          </LocalAccount>
        </LocalAccounts>
      </UserAccounts>
      <AutoLogon>
        <Enabled>true</Enabled>
        <LogonCount>3</LogonCount>
        <Username>` + buildUser + `</Username>
        <Password><Value>`)
	b.WriteString(xmlEscape(password))
	b.WriteString(`</Value><PlainText>true</PlainText></Password>
      </AutoLogon>
      <FirstLogonCommands>
        <SynchronousCommand wcm:action="add"><Order>1</Order><CommandLine>powershell.exe -NoProfile -ExecutionPolicy Bypass -Command "foreach ($d in Get-PSDrive -PSProvider FileSystem) { $p = Join-Path $d.Root '` + provisionName + `'; if (Test-Path $p) { &amp; $p; break } }"</CommandLine></SynchronousCommand>
      </FirstLogonCommands>
    </component>
  </settings>
</unattend>
`)
	return b.String()
}
