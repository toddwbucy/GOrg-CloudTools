package api

// compat_tools.go — Phase 4.5 frontend-compatibility handlers for tool-specific
// execution endpoints (linux-qc-prep, linux-qc-post, sft-fixer, disk-recon,
// rhsa-compliance, decom-survey).
//
// Every tool reduces to the same primitive:
//   POST /aws/{tool}/execute-*  →  runner.Start() or ssm.Send()  →  {batch_id}
//   GET  /aws/{tool}/results/*  →  DB query + shape adapter       →  tool JSON
//
// Disk-recon is the single exception: it sends an SSM command directly and
// polls by command_id rather than batch_id. Every other tool uses runner.Start().

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"

	awscreds "github.com/toddwbucy/GOrg-CloudTools/internal/cloud/aws/credentials"
	awsssm "github.com/toddwbucy/GOrg-CloudTools/internal/cloud/aws/ssm"
	"github.com/toddwbucy/GOrg-CloudTools/internal/api/middleware"
	"github.com/toddwbucy/GOrg-CloudTools/internal/db/models"
	"github.com/toddwbucy/GOrg-CloudTools/internal/exec"
	"gorm.io/gorm"
)

// ── Embedded script constants ─────────────────────────────────────────────────

// Linux QC Prep scripts — templates; {{CHANGE_NUMBER}} and {{KERNEL_VERSION}}
// are substituted at request time with injectQCParams().

const scriptQCStep1 = `CHANGE_NUMBER="{{CHANGE_NUMBER}}"
rm -rf /root/LinuxPatcher
wget -O /root/LinuxPatcher.sh https://pcm-ops-tools.s3.us-gov-west-1.amazonaws.com/linux_patcher/Linux_Patcher_v258-c.sh
bash /root/LinuxPatcher.sh -c $CHANGE_NUMBER -q -s
cat /root/$CHANGE_NUMBER/qc_report.txt`

const scriptQCStep2 = `CHANGE_NUMBER="{{CHANGE_NUMBER}}"
KERNEL_VERSION="{{KERNEL_VERSION}}"
bash /root/LinuxPatcher.sh -c $CHANGE_NUMBER -k $KERNEL_VERSION -q`

const scriptQCStep3 = `CHANGE_NUMBER="{{CHANGE_NUMBER}}"
echo "=== QC Report ==="
cat /root/$CHANGE_NUMBER/qc_report.txt
echo ""
echo "=== Patch Script ==="
cat /root/$CHANGE_NUMBER/patchme.sh`

// Linux QC Post — the change_number drives kernel validation in patchme.sh.
const scriptQCPost = `CHANGE="{{CHANGE_NUMBER}}"

echo "=== BASIC SYSTEM INFO ==="
hostname; date; uptime

echo ""
echo "=== KERNEL VALIDATION ==="
if [[ ! -f "/root/$CHANGE/patchme.sh" ]]; then
    echo "ERROR: /root/$CHANGE/patchme.sh not found"
    exit 1
fi
k_target=$(grep -Eo 'kernel-[0-9]+\.[0-9]+\.[0-9]+-[^ ]+' /root/$CHANGE/patchme.sh | sed 's/^kernel-//' | head -1)
current_kernel=$(uname -r)
if [[ -z "$k_target" ]]; then
    echo "WARNING: Could not extract target kernel"
elif [[ "$current_kernel" == "$k_target"* ]]; then
    echo "✓ PASS: Kernel matches target"
    echo "  Current: $current_kernel"
    echo "  Target:  $k_target"
else
    echo "✗ FAIL: Kernel mismatch"
    echo "  Current: $current_kernel"
    echo "  Target:  $k_target"
fi

echo ""
echo "=== PACKAGE MANAGEMENT ==="
if command -v yum >/dev/null 2>&1; then
    yum history | grep $(date +%F) || echo "No yum activity today"
elif command -v apt >/dev/null 2>&1; then
    grep " $(date +%Y-%m-%d)" /var/log/apt/history.log 2>/dev/null || echo "No apt activity today"
fi

echo ""
echo "=== CRITICAL SERVICES ==="
for svc in sftd falcon-sensor besclient; do
    if systemctl is-active "$svc" >/dev/null 2>&1; then
        echo "✓ $svc: Running"
    elif systemctl status "$svc" >/dev/null 2>&1; then
        echo "✗ $svc: Not Running"
    else
        echo "- $svc: Not installed"
    fi
done`

// Disk recon scripts.
const scriptDiskReconLinux = `#!/bin/bash
HOSTNAME=$(hostname -f 2>/dev/null || hostname)
DATE=$(date '+%Y-%m-%d %H:%M:%S %Z')
INSTANCE_ID=$(curl -sf --connect-timeout 2 http://169.254.169.254/latest/meta-data/instance-id 2>/dev/null || echo 'unknown')

echo "================================================================"
echo " DISK RECON REPORT"
echo "================================================================"
echo " Hostname    : $HOSTNAME"
echo " Instance ID : $INSTANCE_ID"
echo " Date        : $DATE"
echo "================================================================"

echo "--- DISK USAGE (df -hT) ---"
df -hT
echo ""

echo "--- BLOCK DEVICES (lsblk) ---"
lsblk -o NAME,SIZE,TYPE,FSTYPE,MOUNTPOINT 2>/dev/null || lsblk
echo ""

echo "--- LVM PHYSICAL VOLUMES (pvs) ---"
if command -v pvs &>/dev/null; then pvs 2>/dev/null || echo "(no PVs)"; else echo "(LVM not installed)"; fi
echo ""

echo "--- LVM VOLUME GROUPS (vgs) ---"
if command -v vgs &>/dev/null; then vgs 2>/dev/null || echo "(no VGs)"; else echo "(LVM not installed)"; fi
echo ""

echo "--- ACTIVE MOUNTS ---"
mount | grep -v -E '^(tmpfs|proc|sysfs|cgroup|devpts|devtmpfs|overlay|shm) ' | sort
echo ""

echo "--- INODE USAGE (df -ih) ---"
df -ih | grep -v -E '^(tmpfs|devtmpfs|udev)'
echo ""

echo "--- TOP SPACE CONSUMERS ---"
du -hx --max-depth=1 / 2>/dev/null | sort -hr | head -20
echo ""

echo "================================================================"
echo " END DISK RECON REPORT"
echo "================================================================"`

const scriptDiskReconWindows = `$ErrorActionPreference = 'Continue'
$ProgressPreference = 'SilentlyContinue'
$hostname = $env:COMPUTERNAME
$date = Get-Date -Format "yyyy-MM-dd HH:mm:ss"
try {
    $r = Invoke-WebRequest -Uri "http://169.254.169.254/latest/meta-data/instance-id" -UseBasicParsing -TimeoutSec 2
    $instanceId = $r.Content
} catch { $instanceId = "unknown" }

Write-Output "================================================================"
Write-Output " DISK RECON REPORT"
Write-Output "================================================================"
Write-Output " Hostname    : $hostname"
Write-Output " Instance ID : $instanceId"
Write-Output " Date        : $date"
Write-Output "================================================================"

Write-Output "--- DRIVE USAGE ---"
try {
    Get-Volume | Where-Object { $_.DriveType -ne 'CD-ROM' } |
        Select-Object DriveLetter, FileSystemLabel, FileSystem,
            @{N='SizeGB';E={[math]::Round($_.Size/1GB,2)}},
            @{N='FreeGB';E={[math]::Round($_.SizeRemaining/1GB,2)}},
            @{N='UsedPct';E={ if ($_.Size -gt 0) { "$([math]::Round(($_.Size-$_.SizeRemaining)/$_.Size*100,1))%" } else { 'N/A' } }} |
        Sort-Object UsedPct -Descending |
        Format-Table -AutoSize | Out-String -Width 200
} catch { Write-Output "Error: $_" }

Write-Output "--- PHYSICAL DISKS ---"
try {
    Get-Disk | Select-Object Number, FriendlyName,
        @{N='SizeGB';E={[math]::Round($_.Size/1GB,2)}},
        PartitionStyle, HealthStatus | Format-Table -AutoSize | Out-String -Width 200
} catch { Write-Output "Error: $_" }

Write-Output "================================================================"
Write-Output " END DISK RECON REPORT"
Write-Output "================================================================"`

// RHSA compliance script templates. __RHSA_LIST__ / __CVE_LIST__ are replaced
// with a bash array literal: "RHSA-2023:1234" "RHSA-2024:5678"
const rhsaScriptTemplate = `#!/bin/bash
HOSTNAME=$(hostname -f 2>/dev/null || hostname)
DATE=$(date -u '+%Y-%m-%dT%H:%M:%SZ')

if command -v dnf &>/dev/null; then
    PKG_MGR="dnf"
elif command -v yum &>/dev/null; then
    PKG_MGR="yum"
else
    printf '{"error":"no_pkg_mgr","hostname":"%s","date":"%s"}\n' "$HOSTNAME" "$DATE"
    exit 1
fi

INSTALLED=$($PKG_MGR updateinfo list installed 2>/dev/null | awk '/RHSA-/{print $1}' | sort -u || true)
AVAILABLE=$($PKG_MGR updateinfo list available 2>/dev/null | awk '/RHSA-/{print $1}' | sort -u || true)

RHSA_CHECKS=(__RHSA_LIST__)

JSON=""
for ADV in "${RHSA_CHECKS[@]}"; do
    if echo "$INSTALLED" | grep -qF "$ADV" 2>/dev/null; then
        ST="APPLIED"
    elif echo "$AVAILABLE" | grep -qF "$ADV" 2>/dev/null; then
        ST="MISSING"
    else
        ST="N/A"
    fi
    [ -n "$JSON" ] && JSON="${JSON},"
    JSON="${JSON}{\"advisory\":\"${ADV}\",\"status\":\"${ST}\"}"
done

printf '{"hostname":"%s","date":"%s","pkg_mgr":"%s","results":[%s]}\n' \
    "$HOSTNAME" "$DATE" "$PKG_MGR" "$JSON"
`

const cveScriptTemplate = `#!/bin/bash
HOSTNAME=$(hostname -f 2>/dev/null || hostname)
DATE=$(date -u '+%Y-%m-%dT%H:%M:%SZ')

if command -v dnf &>/dev/null; then
    PKG_MGR="dnf"
elif command -v yum &>/dev/null; then
    PKG_MGR="yum"
else
    printf '{"error":"no_pkg_mgr","hostname":"%s","date":"%s"}\n' "$HOSTNAME" "$DATE"
    exit 1
fi

INSTALLED=$($PKG_MGR updateinfo list installed 2>/dev/null | awk '/RHSA-/{print $1}' | sort -u || true)
AVAILABLE=$($PKG_MGR updateinfo list available 2>/dev/null | awk '/RHSA-/{print $1}' | sort -u || true)

CVE_CHECKS=(__CVE_LIST__)

JSON=""
for CVE in "${CVE_CHECKS[@]}"; do
    ADV=$($PKG_MGR updateinfo list --cve "$CVE" 2>/dev/null | awk '/RHSA-/{print $1; exit}' || true)
    if [ -z "$ADV" ]; then
        ITEM="{\"advisory\":\"${CVE}\",\"status\":\"N/A\"}"
    elif echo "$INSTALLED" | grep -qF "$ADV" 2>/dev/null; then
        ITEM="{\"advisory\":\"${CVE}\",\"rhsa\":\"${ADV}\",\"status\":\"APPLIED\"}"
    elif echo "$AVAILABLE" | grep -qF "$ADV" 2>/dev/null; then
        ITEM="{\"advisory\":\"${CVE}\",\"rhsa\":\"${ADV}\",\"status\":\"MISSING\"}"
    else
        ITEM="{\"advisory\":\"${CVE}\",\"rhsa\":\"${ADV}\",\"status\":\"N/A\"}"
    fi
    [ -n "$JSON" ] && JSON="${JSON},"
    JSON="${JSON}${ITEM}"
done

printf '{"hostname":"%s","date":"%s","pkg_mgr":"%s","results":[%s]}\n' \
    "$HOSTNAME" "$DATE" "$PKG_MGR" "$JSON"
`

// SFT fixer scripts. {{TOKEN}} is substituted at request time with sftToken().

const scriptSFTDetectLinux = `#!/bin/bash
echo "=== SFT Detection Script ==="
if systemctl list-unit-files sftd.service >/dev/null 2>&1; then
    echo "SFT_INSTALLED=true"
    systemctl is-active sftd >/dev/null 2>&1 && echo "SFT_STATUS=running" || echo "SFT_STATUS=stopped"
    [ -f "/var/lib/sftd/enrollment.token" ] && echo "SFT_ENROLLMENT_TOKEN=exists" || echo "SFT_ENROLLMENT_TOKEN=missing"
    [ -f "/var/lib/sftd/device.token" ]     && echo "SFT_DEVICE_TOKEN=exists"     || echo "SFT_DEVICE_TOKEN=missing"
    if [ -f "/etc/sft/sftd.yaml" ]; then echo "SFT_CONFIG=exists"; cat /etc/sft/sftd.yaml; else echo "SFT_CONFIG=missing"; fi
else
    echo "SFT_INSTALLED=false"
    [ -f /etc/redhat-release ]  && echo "LINUX_DISTRO=rhel"    || true
    [ -f /etc/debian_version ]  && echo "LINUX_DISTRO=ubuntu"  || true
fi
echo "=== Detection Complete ==="`

const scriptSFTDetectWindows = `Write-Host "=== Windows SFT Detection Script ==="
try {
    $svc = Get-Service -Name "scaleft-server-tools" -ErrorAction SilentlyContinue
    if ($svc) {
        Write-Host "SFT_INSTALLED=true"
        Write-Host "SFT_STATUS=$(if ($svc.Status -eq 'Running') { 'running' } else { 'stopped' })"
        $tp = "C:\Windows\System32\config\systemprofile\AppData\Local\ScaleFT\enrollment.token"
        Write-Host "SFT_ENROLLMENT_TOKEN=$(if (Test-Path $tp) { 'exists' } else { 'missing' })"
    } else {
        Write-Host "SFT_INSTALLED=false"
    }
} catch { Write-Host "Error: $($_.Exception.Message)" }
Write-Host "=== Detection Complete ==="`

const scriptSFTInstallRHEL = `#!/bin/bash
echo "=== RHEL SFT Installation ==="
ipassign=$(for i in $(ip route show default | awk '{print $5}'); do ip addr list $i | grep "inet " | awk '{print $2}' | awk -F"/" '{print $1}'; done)
mkdir -p /etc/sft/ /var/lib/sftd
curl -C - -o /etc/yum.repos.d/scaleft.repo "https://pkg.scaleft.com/scaleft_yum.repo"
rpm --import https://dist.scaleft.com/GPG-KEY-OktaPAM-2023
yum -y install scaleft-server-tools
echo "{{TOKEN}}" > /var/lib/sftd/enrollment.token
echo -e "Autoenroll: false\nAccessAddress: $ipassign" > /etc/sft/sftd.yaml
systemctl enable sftd && systemctl start sftd
echo "=== RHEL SFT Installation Complete ==="
systemctl status sftd --no-pager -l`

const scriptSFTInstallUbuntu = `#!/bin/bash
echo "=== Ubuntu SFT Installation ==="
ipassign=$(for i in $(ip route show default | awk '{print $5}'); do ip addr list $i | grep "inet " | awk '{print $2}' | awk -F"/" '{print $1}'; done)
mkdir -p /etc/sft/ /var/lib/sftd
curl -fsSL https://dist.scaleft.com/GPG-KEY-OktaPAM-2023 | gpg --dearmor | sudo tee /usr/share/keyrings/oktapam-2023-archive-keyring.gpg > /dev/null
echo "deb [signed-by=/usr/share/keyrings/oktapam-2023-archive-keyring.gpg] https://dist.scaleft.com/repos/deb jammy okta" | sudo tee /etc/apt/sources.list.d/oktapam-stable.list
apt-get update && apt-get install -y scaleft-server-tools
echo "{{TOKEN}}" > /var/lib/sftd/enrollment.token
echo -e "Autoenroll: false\nAccessAddress: $ipassign" > /etc/sft/sftd.yaml
systemctl enable sftd && systemctl start sftd
echo "=== Ubuntu SFT Installation Complete ==="
systemctl status sftd --no-pager -l`

const scriptSFTInstallWindows = `Write-Host "=== Windows SFT Installation ==="
Write-Host "Windows SFT installation is not yet automated."
Write-Host "Please install ScaleFT manually, then use the reset function to configure it."`

const scriptSFTResetLinux = `#!/bin/bash
echo "=== Linux SFT Reset ==="
ipassign=$(for i in $(ip route show default | awk '{print $5}'); do ip addr list $i | grep "inet " | awk '{print $2}' | awk -F"/" '{print $1}'; done)
mkdir -p /etc/sft/ /var/lib/sftd
systemctl stop sftd
rm -f /var/lib/sftd/device.token
echo "{{TOKEN}}" > /var/lib/sftd/enrollment.token
echo -e "Autoenroll: false\nAccessAddress: $ipassign" > /etc/sft/sftd.yaml
systemctl start sftd
echo "=== Linux SFT Reset Complete ==="
systemctl status sftd --no-pager -l
cat /etc/sft/sftd.yaml`

// scriptSFTResetWindows uses a regular Go string (not raw) because the
// PowerShell config here-string notation would contain a backtick that would
// otherwise terminate a Go raw string literal.
const scriptSFTResetWindows = "Write-Host \"=== Windows SFT Reset ===\"\n" +
	"try {\n" +
	"    $ip = (Get-NetIPAddress -AddressFamily IPv4 | Where-Object { $_.IPAddress -notlike '127.*' } | Select-Object -First 1).IPAddress\n" +
	"    Stop-Service \"scaleft-server-tools\" -Force\n" +
	"    $dt = \"C:\\Windows\\System32\\config\\systemprofile\\AppData\\Local\\ScaleFT\\state\\device.token\"\n" +
	"    if (Test-Path $dt) { Remove-Item $dt -Force }\n" +
	"    $et = \"C:\\Windows\\System32\\config\\systemprofile\\AppData\\Local\\ScaleFT\\enrollment.token\"\n" +
	"    Set-Content $et \"{{TOKEN}}\"\n" +
	"    $cp = \"C:\\Windows\\System32\\config\\systemprofile\\AppData\\Local\\scaleft\\sftd.yaml\"\n" +
	"    $cfg = \"Autoenroll: false`nAccessAddress: $ip\"\n" +
	"    Set-Content $cp $cfg\n" +
	"    Start-Service \"scaleft-server-tools\"\n" +
	"    Write-Host \"=== Windows SFT Reset Complete ===\"\n" +
	"    (Get-Service \"scaleft-server-tools\").Status\n" +
	"} catch { Write-Host \"Error: $($_.Exception.Message)\"; exit 1 }"

// ── Script generators ─────────────────────────────────────────────────────────

// injectQCParams replaces {{CHANGE_NUMBER}} and {{KERNEL_VERSION}} in a QC
// script template with the provided values.
func injectQCParams(tmpl, changeNumber, kernelVersion string) string {
	r := strings.NewReplacer(
		"{{CHANGE_NUMBER}}", changeNumber,
		"{{KERNEL_VERSION}}", kernelVersion,
	)
	return r.Replace(tmpl)
}

// rhsaScript generates the RHSA advisory check bash script.
func rhsaScript(advisoryIDs []string) string {
	items := make([]string, len(advisoryIDs))
	for i, id := range advisoryIDs {
		items[i] = `"` + id + `"`
	}
	return strings.ReplaceAll(rhsaScriptTemplate, "__RHSA_LIST__", strings.Join(items, " "))
}

// cveScript generates the CVE check bash script.
func cveScript(cveIDs []string) string {
	items := make([]string, len(cveIDs))
	for i, id := range cveIDs {
		items[i] = `"` + id + `"`
	}
	return strings.ReplaceAll(cveScriptTemplate, "__CVE_LIST__", strings.Join(items, " "))
}

// sftToken returns the SFT enrollment token from env, substituted into SFT
// scripts. An empty token will still produce a valid (if unconfigured) script.
func sftToken() string {
	return os.Getenv("SFT_ENROLLMENT_TOKEN")
}

// sftScript returns the script body for the given script_type key,
// with the enrollment token injected. Returns an error for unknown types.
func sftScript(scriptType string) (script, platform string, err error) {
	tok := sftToken()
	inject := func(s string) string {
		return strings.ReplaceAll(s, "{{TOKEN}}", tok)
	}
	switch scriptType {
	case "detect":
		return inject(scriptSFTDetectLinux), "linux", nil
	case "detect_windows":
		return inject(scriptSFTDetectWindows), "windows", nil
	case "install_rhel":
		return inject(scriptSFTInstallRHEL), "linux", nil
	case "install_ubuntu":
		return inject(scriptSFTInstallUbuntu), "linux", nil
	case "install_windows":
		return inject(scriptSFTInstallWindows), "windows", nil
	case "reset_linux":
		return inject(scriptSFTResetLinux), "linux", nil
	case "reset_windows":
		return inject(scriptSFTResetWindows), "windows", nil
	default:
		return "", "", fmt.Errorf("unknown script_type %q", scriptType)
	}
}

// ── Output parsers ────────────────────────────────────────────────────────────

// ansiRE strips ANSI escape sequences from terminal output before parsing.
var ansiRE = regexp.MustCompile(`\x1B(?:[@-Z\\-_]|\[[0-?]*[ -/]*[@-~])`)

// awsAccountIDRE matches a valid AWS account ID: exactly 12 decimal digits.
var awsAccountIDRE = regexp.MustCompile(`^[0-9]{12}$`)

func stripANSI(s string) string { return ansiRE.ReplaceAllString(s, "") }

// qcInfo holds fields extracted from a LinuxPatcher QC report.
type qcInfo struct {
	Hostname          string
	CurrentKernel     string
	Distro            string
	CrowdstrikeOK     bool
	CrowdstrikeVer    string
	AvailableKernels  []string
	TestPassed        bool
	DiskSpacePassed   bool
	QCPassed          bool // both test AND disk must pass
}

// parseQCOutput extracts key fields from a LinuxPatcher QC report text.
func parseQCOutput(raw string) qcInfo {
	clean := stripANSI(raw)
	lines := strings.Split(clean, "\n")
	var info qcInfo

	for i, line := range lines {
		line = strings.TrimSpace(line)

		if strings.Contains(line, "===== QC report for") {
			parts := strings.Split(line, "QC report for")
			if len(parts) > 1 {
				info.Hostname = strings.Trim(strings.Split(parts[1], "=====")[0], " ")
			}
		} else if strings.Contains(line, "(Current running kernel version):") {
			if idx := strings.Index(line, ":"); idx >= 0 {
				k := strings.TrimSpace(line[idx+1:])
				info.CurrentKernel = k
				switch {
				case strings.Contains(k, ".el9"):
					info.Distro = "RHEL9"
				case strings.Contains(k, ".el8_10"):
					info.Distro = "RHEL8.10"
				case strings.Contains(k, ".el8_9"):
					info.Distro = "RHEL8.9"
				case strings.Contains(k, ".el8_8"):
					info.Distro = "RHEL8.8"
				case strings.Contains(k, ".el8"):
					info.Distro = "RHEL8"
				case strings.Contains(k, ".el7"):
					info.Distro = "RHEL7"
				case strings.Contains(k, ".amzn2023"):
					info.Distro = "Amazon Linux 2023"
				case strings.Contains(k, ".amzn2"):
					info.Distro = "Amazon Linux 2"
				case strings.Contains(k, ".amzn1"):
					info.Distro = "Amazon Linux 1"
				}
			}
		} else if strings.Contains(line, "(Is Crowdstrike running):") {
			info.CrowdstrikeOK = strings.Contains(strings.ToLower(line), "yes")
		} else if strings.Contains(line, "(Current Crowdstrike Version):") {
			if idx := strings.Index(line, ":"); idx >= 0 {
				v := strings.TrimSpace(line[idx+1:])
				if strings.Contains(v, "=") {
					v = strings.TrimSpace(strings.SplitN(v, "=", 2)[1])
				}
				info.CrowdstrikeVer = v
			}
		} else if strings.Contains(line, "(Available Kernel Updates):") {
			for j := i + 1; j < len(lines); j++ {
				kl := strings.TrimSpace(lines[j])
				if kl == "" && len(info.AvailableKernels) > 0 {
					break
				}
				if strings.HasPrefix(kl, "(") {
					break
				}
				if strings.HasPrefix(kl, "kernel") {
					parts := strings.Fields(kl)
					if len(parts) >= 2 {
						info.AvailableKernels = append(info.AvailableKernels, parts[1])
					}
				}
			}
		} else if strings.Contains(line, "(Test Repositories Result):") {
			info.TestPassed = strings.Contains(strings.ToUpper(line), "PASSED")
		} else if strings.Contains(line, "(Disk Space Check Result):") {
			info.DiskSpacePassed = strings.Contains(strings.ToUpper(line), "PASSED")
		}
	}
	info.QCPassed = info.TestPassed && info.DiskSpacePassed
	return info
}

// complianceOutput is the JSON shape the RHSA/CVE bash script prints to stdout.
type complianceOutput struct {
	Hostname string `json:"hostname"`
	Date     string `json:"date"`
	PkgMgr   string `json:"pkg_mgr"`
	Error    string `json:"error"`
	Results  []struct {
		Advisory string `json:"advisory"`
		RHSA     string `json:"rhsa,omitempty"`
		Status   string `json:"status"`
	} `json:"results"`
}

// parseComplianceOutput finds the last JSON line in raw stdout and parses it
// into a compliance result map suitable for JSON serialisation to the frontend.
func parseComplianceOutput(raw string) map[string]any {
	clean := stripANSI(raw)
	lines := strings.Split(strings.TrimSpace(clean), "\n")

	// Scan in reverse for the last JSON line.
	var parsed complianceOutput
	found := false
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if strings.HasPrefix(l, "{") {
			if err := json.Unmarshal([]byte(l), &parsed); err == nil {
				found = true
				break
			}
		}
	}
	if !found {
		return map[string]any{
			"compliance_status": "UNKNOWN",
			"error":             "could not parse script output as JSON",
		}
	}
	if parsed.Error != "" {
		return map[string]any{
			"compliance_status": "ERROR",
			"error":             parsed.Error,
			"hostname":          parsed.Hostname,
		}
	}

	applied, missing, na := 0, 0, 0
	var missingIDs []string
	for _, r := range parsed.Results {
		switch r.Status {
		case "APPLIED":
			applied++
		case "MISSING":
			missing++
			missingIDs = append(missingIDs, r.Advisory)
		default:
			na++
		}
	}
	status := "COMPLIANT"
	if missing > 0 {
		status = "NON_COMPLIANT"
	}
	if missingIDs == nil {
		missingIDs = []string{}
	}
	return map[string]any{
		"hostname":             parsed.Hostname,
		"date":                 parsed.Date,
		"pkg_mgr":              parsed.PkgMgr,
		"results":              parsed.Results,
		"applied":              applied,
		"missing":              missing,
		"na":                   na,
		"missing_advisories":   missingIDs,
		"compliance_status":    status,
	}
}

// parsePostPatchOutput does a simple pass/fail detection for the QC post script
// output, looking for the kernel validation result lines.
func parsePostPatchOutput(raw string) map[string]any {
	clean := stripANSI(raw)
	kernelPass := strings.Contains(clean, "✓ PASS: Kernel matches target")
	kernelFail := strings.Contains(clean, "✗ FAIL: Kernel mismatch")

	// Extract hostname (first line from `hostname` command output).
	hostname := ""
	for _, l := range strings.Split(clean, "\n") {
		l = strings.TrimSpace(l)
		if l != "" && !strings.HasPrefix(l, "===") {
			hostname = l
			break
		}
	}

	// Extract current kernel from FAIL line if present.
	currentKernel := ""
	for _, l := range strings.Split(clean, "\n") {
		if strings.Contains(l, "Current:") {
			currentKernel = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "Current:"))
			break
		}
	}

	return map[string]any{
		"hostname":         hostname,
		"current_kernel":   currentKernel,
		"kernel_match":     kernelPass,
		"kernel_fail":      kernelFail,
		"validation_passed": kernelPass,
	}
}

// ── Shared DB helpers ─────────────────────────────────────────────────────────

// loadSessionChange loads the Change + Instances for the ID stored in the
// session cookie. Returns 400-worthy error when no change is in session,
// gorm.ErrRecordNotFound when the change ID no longer exists.
func (s *Server) loadSessionChange(sess *middleware.Session) (*models.Change, error) {
	if sess.CurrentChangeID == 0 {
		return nil, fmt.Errorf("no change loaded in session")
	}
	var ch models.Change
	err := s.db.Preload("Instances").First(&ch, sess.CurrentChangeID).Error
	return &ch, err
}

// loadToolBatch loads an ExecutionBatch by its numeric ID string, verifying
// caller ownership via CallerKey. Returns 404 or 500 via jsonError on failure.
// Returns nil (and writes the error response) on any error so callers can
// `if batch == nil { return }`.
func (s *Server) loadToolBatch(w http.ResponseWriter, batchIDStr, callerKey string) *models.ExecutionBatch {
	var batch models.ExecutionBatch
	err := s.db.Preload("Executions").
		Where("id = ? AND caller_key = ?", batchIDStr, callerKey).
		First(&batch).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			jsonError(w, "batch not found", http.StatusNotFound)
		} else {
			jsonError(w, "database error", http.StatusInternalServerError)
		}
		return nil
	}
	return &batch
}

// startToolJob is a thin wrapper around runner.Start that builds the
// exec.ScriptRequest for a tool execution and returns the new batch ID.
// All validation (credentials, instance resolution) must be done by the caller.
func (s *Server) startToolJob(
	r *http.Request,
	script, platform, accountID, region, changeNumber, callerKey string,
	instanceIDs []string,
) (uint, error) {
	sess := middleware.GetSession(r)
	cfg, _, err := awscreds.FromSession(r.Context(), sess)
	if err != nil {
		return 0, fmt.Errorf("no valid AWS credentials: %w", err)
	}

	runner := exec.New(s.db, s.cfg.MaxConcurrentExecutions, s.cfg.ExecutionTimeoutSecs)
	return runner.Start(r.Context(), cfg, exec.ScriptRequest{
		InlineScript: script,
		Platform:     platform,
		InstanceIDs:  instanceIDs,
		AccountID:    accountID,
		Region:       region,
		ChangeNumber: changeNumber,
		CallerKey:    callerKey,
	})
}

// resolveChangeInstances returns the instance IDs, accountID, and region from
// the change's instances, filtered to the requested subset. All instances must
// resolve to the same (account, region) pair (SSM is region-scoped).
func resolveChangeInstances(ch *models.Change, requestedIDs []string) (
	ids []string, accountID, region string, err error,
) {
	want := make(map[string]bool, len(requestedIDs))
	for _, id := range requestedIDs {
		want[id] = true
	}

	type pair struct{ account, region string }
	seen := make(map[pair]struct{})
	var resolved pair

	for _, ci := range ch.Instances {
		if len(want) > 0 && !want[ci.InstanceID] {
			continue
		}
		ids = append(ids, ci.InstanceID)
		p := pair{ci.AccountID, ci.Region}
		seen[p] = struct{}{}
		resolved = p
	}
	if len(ids) == 0 {
		return nil, "", "", fmt.Errorf("no matching instances found in change")
	}
	if len(seen) > 1 {
		return nil, "", "", errCrossRegion
	}
	return ids, resolved.account, resolved.region, nil
}

// ── Linux QC Prep ─────────────────────────────────────────────────────────────

type qcStepRequest struct {
	Step          string `json:"step"`
	KernelVersion string `json:"kernel_version"`
}

// handleQCStep executes a named QC step (step1_initial_qc, step2_kernel_staging,
// step3_final_report) on all instances in the current session change.
//
// Route: POST /aws/linux-qc-prep/execute-qc-step
func (s *Server) handleQCStep(w http.ResponseWriter, r *http.Request) {
	var req qcStepRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	var scriptTmpl string
	switch req.Step {
	case "step1_initial_qc":
		scriptTmpl = scriptQCStep1
	case "step2_kernel_staging":
		if strings.TrimSpace(req.KernelVersion) == "" {
			jsonError(w, "kernel_version is required for step2_kernel_staging", http.StatusBadRequest)
			return
		}
		scriptTmpl = scriptQCStep2
	case "step3_final_report":
		scriptTmpl = scriptQCStep3
	default:
		jsonError(w, fmt.Sprintf("unknown step %q: must be step1_initial_qc, step2_kernel_staging, or step3_final_report", req.Step), http.StatusBadRequest)
		return
	}

	sess := middleware.GetSession(r)
	ch, err := s.loadSessionChange(sess)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			jsonError(w, "change not found", http.StatusBadRequest)
		} else if sess.CurrentChangeID == 0 {
			jsonError(w, "no change loaded in session", http.StatusBadRequest)
		} else {
			jsonError(w, "database error", http.StatusInternalServerError)
		}
		return
	}

	instanceIDs, accountID, region, err := resolveChangeInstances(ch, nil)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	script := injectQCParams(scriptTmpl, ch.ChangeNumber, req.KernelVersion)

	batchID, err := s.startToolJob(r, script, "linux", accountID, region, ch.ChangeNumber, sess.AWSAccessKeyID, instanceIDs)
	if err != nil {
		slog.Error("QC step runner.Start failed", "step", req.Step, "err", err)
		jsonError(w, "failed to start execution", http.StatusInternalServerError)
		return
	}

	jsonOK(w, map[string]any{
		"status":          "success",
		"batch_id":        batchID,
		"execution_count": len(instanceIDs),
		"step":            req.Step,
	})
}

// handleQCResults returns batch progress and parsed QC output for the given
// batch. For step1 batches it also builds kernel_groups for the step2 UI.
//
// Route: GET /aws/linux-qc-prep/qc-results/{batch_id}
func (s *Server) handleQCResults(w http.ResponseWriter, r *http.Request) {
	sess := middleware.GetSession(r)
	batch := s.loadToolBatch(w, r.PathValue("batch_id"), sess.AWSAccessKeyID)
	if batch == nil {
		return
	}

	type resultRow struct {
		InstanceID   string  `json:"instance_id"`
		AccountID    string  `json:"account_id"`
		Region       string  `json:"region"`
		Status       string  `json:"status"`
		Output       string  `json:"output"`
		Error        string  `json:"error"`
		ParsedInfo   any     `json:"parsed_info"`
		InstanceName string  `json:"instance_name"`
	}

	results := make([]resultRow, 0, len(batch.Executions))
	// kernel_groups: group_key → {distro, base_kernel, available_kernels, instances}
	kernelGroups := map[string]any{}
	completed := 0

	for _, ex := range batch.Executions {
		if ex.Status == models.ExecutionStatusCompleted || ex.Status == models.ExecutionStatusFailed {
			completed++
		}

		var parsed any
		if ex.Status == models.ExecutionStatusCompleted {
			info := parseQCOutput(ex.Output)
			parsed = info

			if info.QCPassed && info.CurrentKernel != "" && info.Distro != "" {
				baseParts := strings.SplitN(info.CurrentKernel, ".", 4)
				baseKernel := info.CurrentKernel
				if len(baseParts) >= 3 {
					baseKernel = strings.Join(baseParts[:3], ".")
				}
				groupKey := info.Distro + " - Kernel " + baseKernel

				existing, ok := kernelGroups[groupKey].(map[string]any)
				if !ok {
					existing = map[string]any{
						"distro":            info.Distro,
						"base_kernel":       baseKernel,
						"available_kernels": info.AvailableKernels,
						"instances":         []any{},
						"selected_kernel":   nil,
					}
				}
				// Merge available_kernels (defensive: handle both []string and []interface{}).
				kset := make(map[string]bool)
				switch v := existing["available_kernels"].(type) {
				case []string:
					for _, k := range v {
						kset[k] = true
					}
				case []any:
					for _, k := range v {
						if s, ok := k.(string); ok {
							kset[s] = true
						}
					}
				}
				for _, k := range info.AvailableKernels {
					kset[k] = true
				}
				merged := make([]string, 0, len(kset))
				for k := range kset {
					merged = append(merged, k)
				}
				existing["available_kernels"] = merged

				instances, _ := existing["instances"].([]any)
				instances = append(instances, map[string]any{
					"instance_id":        ex.InstanceID,
					"account_id":         ex.AccountID,
					"region":             ex.Region,
					"hostname":           info.Hostname,
					"current_kernel":     info.CurrentKernel,
					"crowdstrike":        info.CrowdstrikeOK,
					"crowdstrike_version": info.CrowdstrikeVer,
				})
				existing["instances"] = instances
				kernelGroups[groupKey] = existing
			}
		}

		results = append(results, resultRow{
			InstanceID: ex.InstanceID,
			AccountID:  ex.AccountID,
			Region:     ex.Region,
			Status:     string(ex.Status),
			Output:     ex.Output,
			Error:      ex.Error,
			ParsedInfo: parsed,
		})
	}

	jsonOK(w, map[string]any{
		"status":        "success",
		"results":       results,
		"kernel_groups": kernelGroups,
		"total":         len(batch.Executions),
		"completed":     completed,
	})
}

// handleQCLatestStep1Results finds the most recent step1 batch for the current
// session change and returns its kernel_groups. Called on page load to
// auto-populate the step2 UI if a previous step1 run exists.
//
// Route: GET /aws/linux-qc-prep/latest-step1-results
func (s *Server) handleQCLatestStep1Results(w http.ResponseWriter, r *http.Request) {
	sess := middleware.GetSession(r)
	ch, err := s.loadSessionChange(sess)
	if err != nil {
		// No change loaded — return empty success so the page can load normally.
		jsonOK(w, map[string]any{"status": "no_change", "kernel_groups": map[string]any{}})
		return
	}

	// Find the most recent batch whose executions are tagged with this
	// change_number AND were run as step1 (identified via execution_metadata).
	// SQLite's json_extract is the only way to query the serialised map field.
	var batchID uint
	s.db.Raw(`
		SELECT batch_id FROM executions
		WHERE change_number = ?
		  AND json_extract(execution_metadata, '$.qc_step') = 'step1_initial_qc'
		ORDER BY created_at DESC LIMIT 1`, ch.ChangeNumber).Scan(&batchID)

	if batchID == 0 {
		jsonOK(w, map[string]any{"status": "no_results", "kernel_groups": map[string]any{}})
		return
	}

	// Reuse the results handler logic by synthesising a fake path value.
	// Simpler: load the batch directly and call the shared parsing logic.
	var batch models.ExecutionBatch
	if err := s.db.Preload("Executions").
		Where("id = ? AND caller_key = ?", batchID, sess.AWSAccessKeyID).
		First(&batch).Error; err != nil {
		jsonOK(w, map[string]any{"status": "no_results", "kernel_groups": map[string]any{}})
		return
	}

	kernelGroups := map[string]any{}
	for _, ex := range batch.Executions {
		if ex.Status != models.ExecutionStatusCompleted {
			continue
		}
		info := parseQCOutput(ex.Output)
		if !info.QCPassed || info.CurrentKernel == "" || info.Distro == "" {
			continue
		}
		baseParts := strings.SplitN(info.CurrentKernel, ".", 4)
		baseKernel := info.CurrentKernel
		if len(baseParts) >= 3 {
			baseKernel = strings.Join(baseParts[:3], ".")
		}
		groupKey := info.Distro + " - Kernel " + baseKernel

		existing, ok := kernelGroups[groupKey].(map[string]any)
		if !ok {
			existing = map[string]any{
				"distro":            info.Distro,
				"base_kernel":       baseKernel,
				"available_kernels": info.AvailableKernels,
				"instances":         []any{},
				"selected_kernel":   nil,
			}
		}
		existing["instances"] = append(existing["instances"].([]any), map[string]any{
			"instance_id":    ex.InstanceID,
			"hostname":       info.Hostname,
			"current_kernel": info.CurrentKernel,
		})
		kernelGroups[groupKey] = existing
	}

	jsonOK(w, map[string]any{
		"status":        "success",
		"batch_id":      batchID,
		"kernel_groups": kernelGroups,
	})
}

// handleQCDownload returns 501 — the LinuxPatcher PDF/ZIP report rendering is
// not ported to Go. Raw execution output is available via the results endpoint.
//
// Routes: GET /aws/linux-qc-prep/download-reports
//
//	GET /aws/linux-qc-prep/download-final-report
func handleQCDownload(w http.ResponseWriter, _ *http.Request) {
	jsonError(w, "report download not implemented; use /aws/linux-qc-prep/qc-results/{batch_id} to fetch raw output", http.StatusNotImplemented)
}

// ── Linux QC Post ─────────────────────────────────────────────────────────────

type qcPostExecRequest struct {
	InstanceIDs []string `json:"instance_ids"`
}

// handleLinuxQCPostExec runs the post-patch validation script on the supplied
// instance IDs. The instance IDs are resolved via the current session change
// for account/region metadata; the change_number is injected into the script.
//
// Route: POST /aws/linux-qc-post/execute-post-validation
func (s *Server) handleLinuxQCPostExec(w http.ResponseWriter, r *http.Request) {
	var req qcPostExecRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if len(req.InstanceIDs) == 0 {
		jsonError(w, "instance_ids must not be empty", http.StatusBadRequest)
		return
	}

	sess := middleware.GetSession(r)
	ch, err := s.loadSessionChange(sess)
	if err != nil {
		if sess.CurrentChangeID == 0 {
			jsonError(w, "no change loaded in session", http.StatusBadRequest)
		} else {
			jsonError(w, "database error", http.StatusInternalServerError)
		}
		return
	}

	instanceIDs, accountID, region, err := resolveChangeInstances(ch, req.InstanceIDs)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	script := injectQCParams(scriptQCPost, ch.ChangeNumber, "")

	batchID, err := s.startToolJob(r, script, "linux", accountID, region, ch.ChangeNumber, sess.AWSAccessKeyID, instanceIDs)
	if err != nil {
		slog.Error("linux-qc-post runner.Start failed", "err", err)
		jsonError(w, "failed to start execution", http.StatusInternalServerError)
		return
	}

	jsonOK(w, map[string]any{
		"status":          "success",
		"batch_id":        batchID,
		"execution_count": len(instanceIDs),
	})
}

// handleLinuxQCPostResults returns post-patch validation results for a batch,
// categorising instances into passed/failed based on the script output.
//
// Route: GET /aws/linux-qc-post/validation-results/{batch_id}
func (s *Server) handleLinuxQCPostResults(w http.ResponseWriter, r *http.Request) {
	sess := middleware.GetSession(r)
	batch := s.loadToolBatch(w, r.PathValue("batch_id"), sess.AWSAccessKeyID)
	if batch == nil {
		return
	}

	type result struct {
		InstanceID string `json:"instance_id"`
		AccountID  string `json:"account_id"`
		Region     string `json:"region"`
		Status     string `json:"status"`
		Output     string `json:"output"`
		Error      string `json:"error"`
		ParsedInfo any    `json:"parsed_info"`
	}

	results := make([]result, 0, len(batch.Executions))
	var passedInstances, failedInstances []any
	completed := 0

	for _, ex := range batch.Executions {
		if ex.Status == models.ExecutionStatusCompleted || ex.Status == models.ExecutionStatusFailed {
			completed++
		}
		parsed := parsePostPatchOutput(ex.Output)
		results = append(results, result{
			InstanceID: ex.InstanceID,
			AccountID:  ex.AccountID,
			Region:     ex.Region,
			Status:     string(ex.Status),
			Output:     ex.Output,
			Error:      ex.Error,
			ParsedInfo: parsed,
		})

		if ex.Status == models.ExecutionStatusCompleted {
			inst := map[string]any{
				"instance_id":    ex.InstanceID,
				"account_id":     ex.AccountID,
				"region":         ex.Region,
				"hostname":       parsed["hostname"],
				"current_kernel": parsed["current_kernel"],
			}
			if parsed["validation_passed"] == true {
				passedInstances = append(passedInstances, inst)
			} else {
				failedInstances = append(failedInstances, inst)
			}
		}
	}

	if passedInstances == nil {
		passedInstances = []any{}
	}
	if failedInstances == nil {
		failedInstances = []any{}
	}

	jsonOK(w, map[string]any{
		"status":           "success",
		"results":          results,
		"passed_instances": passedInstances,
		"failed_instances": failedInstances,
		"total":            len(batch.Executions),
		"completed":        completed,
		"passed_count":     len(passedInstances),
		"failed_count":     len(failedInstances),
	})
}

// ── SFT Fixer ─────────────────────────────────────────────────────────────────

type sftValidateRequest struct {
	InstanceID    string `json:"instance_id"`
	AccountNumber string `json:"account_number"`
	Region        string `json:"region"`
	OsType        string `json:"os_type"`
}

// handleSFTValidateInstance checks SSM connectivity for a single instance.
// The SFT fixer tool targets a single instance configured manually in the UI
// (not via a loaded change).
//
// Route: POST /aws/sft-fixer/validate-instance
func (s *Server) handleSFTValidateInstance(w http.ResponseWriter, r *http.Request) {
	var req sftValidateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.InstanceID == "" || req.AccountNumber == "" || req.Region == "" {
		jsonError(w, "instance_id, account_number, and region are required", http.StatusBadRequest)
		return
	}

	sess := middleware.GetSession(r)
	cfg, _, err := awscreds.FromSession(r.Context(), sess)
	if err != nil {
		jsonError(w, "no valid AWS credentials in session", http.StatusUnauthorized)
		return
	}
	cfg.Region = req.Region

	online, err := ssmOnlineSet(r.Context(), cfg, []string{req.InstanceID})
	if err != nil {
		jsonError(w, "connectivity check failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if online[req.InstanceID] {
		jsonOK(w, map[string]any{
			"status":  "success",
			"message": fmt.Sprintf("SSM connectivity confirmed for instance %s", req.InstanceID),
		})
	} else {
		jsonError(w, fmt.Sprintf("SSM agent not reachable for %s", req.InstanceID), http.StatusBadRequest)
	}
}

type sftExecRequest struct {
	InstanceConfig struct {
		InstanceID    string `json:"instance_id"`
		AccountNumber string `json:"account_number"`
		Region        string `json:"region"`
		OsType        string `json:"os_type"`
	} `json:"instance_config"`
	ScriptType string `json:"script_type"`
}

// handleSFTExecScript runs the requested SFT script on the target instance.
//
// Route: POST /aws/sft-fixer/execute-script
func (s *Server) handleSFTExecScript(w http.ResponseWriter, r *http.Request) {
	var req sftExecRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.InstanceConfig.InstanceID == "" {
		jsonError(w, "instance_config.instance_id is required", http.StatusBadRequest)
		return
	}
	if req.ScriptType == "" {
		jsonError(w, "script_type is required", http.StatusBadRequest)
		return
	}

	script, platform, err := sftScript(req.ScriptType)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	region := req.InstanceConfig.Region
	accountID := req.InstanceConfig.AccountNumber
	instanceID := req.InstanceConfig.InstanceID

	sess := middleware.GetSession(r)
	batchID, err := s.startToolJob(r, script, platform, accountID, region, "", sess.AWSAccessKeyID, []string{instanceID})
	if err != nil {
		slog.Error("sft-fixer runner.Start failed", "script_type", req.ScriptType, "err", err)
		jsonError(w, "failed to start execution", http.StatusInternalServerError)
		return
	}

	jsonOK(w, map[string]any{
		"status":   "success",
		"batch_id": batchID,
		"message":  fmt.Sprintf("Executing %s on %s", req.ScriptType, instanceID),
	})
}

// handleSFTBatchStatus returns the execution status for an SFT batch in the
// shape the sft-fixer.js polling loop expects:
//
//	{status: "success", batch_status: {status, completed_count, results: [...]}}
//
// Route: GET /aws/sft-fixer/batch-status/{batch_id}
func (s *Server) handleSFTBatchStatus(w http.ResponseWriter, r *http.Request) {
	sess := middleware.GetSession(r)
	batch := s.loadToolBatch(w, r.PathValue("batch_id"), sess.AWSAccessKeyID)
	if batch == nil {
		return
	}

	total := len(batch.Executions)
	completedCount := 0
	results := make([]any, 0, total)

	for _, ex := range batch.Executions {
		done := ex.Status == models.ExecutionStatusCompleted || ex.Status == models.ExecutionStatusFailed
		if done {
			completedCount++
		}
		st := "running"
		if ex.Status == models.ExecutionStatusCompleted {
			st = "success"
		} else if ex.Status == models.ExecutionStatusFailed {
			st = "failed"
		}
		results = append(results, map[string]any{
			"instance_id": ex.InstanceID,
			"status":      st,
			"output":      ex.Output,
		})
	}

	overallStatus := string(batch.Status)
	if batch.Status == models.BatchStatusCompleted {
		overallStatus = "completed"
	}

	jsonOK(w, map[string]any{
		"status": "success",
		"batch_status": map[string]any{
			"status":          overallStatus,
			"completed_count": completedCount,
			"total_count":     total,
			"results":         results,
		},
	})
}

// ── Disk Recon ────────────────────────────────────────────────────────────────
//
// Disk-recon bypasses the batch system entirely. The frontend sends a POST to
// /run to launch the SSM command, receives a command_id, and polls /poll/{id}
// until the status is terminal. No ExecutionBatch record is created.

type diskReconRunRequest struct {
	Environment string `json:"environment"`
	AccountID   string `json:"account_id"`
	Region      string `json:"region"`
	InstanceID  string `json:"instance_id"`
	OsType      string `json:"os_type"`
}

// handleDiskReconRun issues an SSM SendCommand for the disk recon script and
// returns the command_id immediately. All polling is via handleDiskReconPoll.
//
// Route: POST /aws/disk-recon/run
func (s *Server) handleDiskReconRun(w http.ResponseWriter, r *http.Request) {
	var req diskReconRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Validate fields.
	var errs []string
	if !awsAccountIDRE.MatchString(req.AccountID) {
		errs = append(errs, "account_id must be a 12-digit number")
	}
	if req.Region == "" {
		errs = append(errs, "region is required")
	}
	if !strings.HasPrefix(req.InstanceID, "i-") {
		errs = append(errs, "instance_id must start with 'i-'")
	}
	osType := strings.ToLower(strings.TrimSpace(req.OsType))
	if osType == "" {
		osType = "linux"
	}
	if osType != "linux" && osType != "windows" {
		errs = append(errs, "os_type must be 'linux' or 'windows'")
	}
	if len(errs) > 0 {
		jsonError(w, strings.Join(errs, " | "), http.StatusBadRequest)
		return
	}

	sess := middleware.GetSession(r)
	cfg, _, err := awscreds.FromSession(r.Context(), sess)
	if err != nil {
		jsonError(w, "no valid AWS credentials in session", http.StatusUnauthorized)
		return
	}
	cfg.Region = req.Region

	script := scriptDiskReconLinux
	platform := "linux"
	if osType == "windows" {
		script = scriptDiskReconWindows
		platform = "windows"
	}

	executor := awsssm.New(cfg, s.cfg.ExecutionTimeoutSecs)
	commandID, err := executor.Send(r.Context(), []string{req.InstanceID}, script, platform)
	if err != nil {
		jsonError(w, "SSM send failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	jsonOK(w, map[string]any{
		"command_id":  commandID,
		"instance_id": req.InstanceID,
	})
}

// terminalSSMStatuses is the set of SSM command statuses that indicate the
// command has finished (successfully or not).
var terminalSSMStatuses = map[string]bool{
	"Success": true, "Failed": true, "Cancelled": true, "TimedOut": true,
	"DeliveryTimedOut": true, "ExecutionTimedOut": true, "Undeliverable": true,
	"Terminated": true, "InvalidPlatform": true, "AccessDenied": true,
	"Error": true,
}

// handleDiskReconPoll polls the SSM command status and returns the raw result.
// The frontend uses the `terminal` flag to know when to stop polling.
//
// Route: GET /aws/disk-recon/poll/{command_id}
// Query params: instance_id, account_id, region (required)
func (s *Server) handleDiskReconPoll(w http.ResponseWriter, r *http.Request) {
	commandID := r.PathValue("command_id")
	q := r.URL.Query()
	instanceID := strings.TrimSpace(q.Get("instance_id"))
	region := strings.TrimSpace(q.Get("region"))

	if commandID == "" || instanceID == "" || region == "" {
		jsonError(w, "command_id path param and instance_id, region query params are required", http.StatusBadRequest)
		return
	}

	sess := middleware.GetSession(r)
	cfg, _, err := awscreds.FromSession(r.Context(), sess)
	if err != nil {
		jsonError(w, "no valid AWS credentials in session", http.StatusUnauthorized)
		return
	}
	cfg.Region = region

	executor := awsssm.New(cfg, s.cfg.ExecutionTimeoutSecs)
	status, err := executor.GetStatus(r.Context(), commandID, instanceID)
	if err != nil {
		jsonError(w, "poll failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	terminal := terminalSSMStatuses[status.Status]
	jsonOK(w, map[string]any{
		"status":   status.Status,
		"terminal": terminal,
		"output":   status.Output,
		"error":    status.Error,
		"details":  "",
	})
}

// ── RHSA Compliance ───────────────────────────────────────────────────────────

var (
	rhsaIDRE = regexp.MustCompile(`^RHSA-\d{4}:\d+$`)
	cveIDRE  = regexp.MustCompile(`^CVE-\d{4}-\d{4,}$`)
)

type rhsaExecRequest struct {
	CheckType   string   `json:"check_type"`
	AdvisoryIDs []string `json:"advisory_ids"`
	InstanceIDs []string `json:"instance_ids"`
}

// handleRHSAExecute generates and runs an RHSA or CVE compliance check script
// against the requested instance IDs.
//
// Route: POST /aws/rhsa-compliance/execute
func (s *Server) handleRHSAExecute(w http.ResponseWriter, r *http.Request) {
	var req rhsaExecRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	checkType := strings.ToLower(strings.TrimSpace(req.CheckType))
	if checkType == "" {
		checkType = "rhsa"
	}
	if checkType != "rhsa" && checkType != "cve" {
		jsonError(w, "check_type must be 'rhsa' or 'cve'", http.StatusBadRequest)
		return
	}

	if len(req.AdvisoryIDs) == 0 {
		jsonError(w, "advisory_ids must not be empty", http.StatusBadRequest)
		return
	}

	// Normalise and validate IDs.
	pattern := rhsaIDRE
	if checkType == "cve" {
		pattern = cveIDRE
	}
	advisoryIDs := make([]string, 0, len(req.AdvisoryIDs))
	seen := make(map[string]bool)
	var invalid []string
	for _, id := range req.AdvisoryIDs {
		id = strings.TrimSpace(strings.ToUpper(id))
		if id == "" {
			continue
		}
		if !pattern.MatchString(id) {
			invalid = append(invalid, id)
			continue
		}
		if !seen[id] {
			seen[id] = true
			advisoryIDs = append(advisoryIDs, id)
		}
	}
	if len(invalid) > 0 {
		msg := strings.Join(invalid, ", ")
		if len(invalid) > 5 {
			msg = strings.Join(invalid[:5], ", ") + "..."
		}
		jsonError(w, "invalid IDs: "+msg, http.StatusBadRequest)
		return
	}
	if len(advisoryIDs) == 0 {
		jsonError(w, "no valid advisory_ids provided", http.StatusBadRequest)
		return
	}

	if len(req.InstanceIDs) == 0 {
		jsonError(w, "instance_ids must not be empty", http.StatusBadRequest)
		return
	}

	accountID, region, err := s.resolveUniformMeta(req.InstanceIDs, middleware.GetSession(r))
	if err != nil {
		if errors.Is(err, errCrossRegion) {
			jsonError(w, err.Error(), http.StatusBadRequest)
		} else {
			jsonError(w, "failed to resolve instance metadata", http.StatusInternalServerError)
		}
		return
	}

	var script string
	if checkType == "cve" {
		script = cveScript(advisoryIDs)
	} else {
		script = rhsaScript(advisoryIDs)
	}

	sess := middleware.GetSession(r)
	batchID, err := s.startToolJob(r, script, "linux", accountID, region, "", sess.AWSAccessKeyID, req.InstanceIDs)
	if err != nil {
		slog.Error("rhsa runner.Start failed", "check_type", checkType, "err", err)
		jsonError(w, "failed to start execution", http.StatusInternalServerError)
		return
	}

	jsonOK(w, map[string]any{
		"batch_id":        batchID,
		"execution_count": len(req.InstanceIDs),
	})
}

// handleRHSAResults returns compliance check results, parsing the JSON line
// that the bash script emits to stdout for each instance.
//
// Route: GET /aws/rhsa-compliance/results/{batch_id}
func (s *Server) handleRHSAResults(w http.ResponseWriter, r *http.Request) {
	sess := middleware.GetSession(r)
	batch := s.loadToolBatch(w, r.PathValue("batch_id"), sess.AWSAccessKeyID)
	if batch == nil {
		return
	}

	counts := map[string]int{
		"pending": 0, "running": 0, "completed": 0, "failed": 0, "interrupted": 0,
	}
	results := make([]any, 0, len(batch.Executions))

	for _, ex := range batch.Executions {
		st := string(ex.Status)
		if _, ok := counts[st]; ok {
			counts[st]++
		}

		var compliance map[string]any
		if ex.Status == models.ExecutionStatusCompleted || ex.Status == models.ExecutionStatusFailed {
			compliance = parseComplianceOutput(ex.Output)
			// If the script exited non-zero and we have no parsed compliance results,
			// override to ERROR. parseComplianceOutput already handles this for JSON
			// parse failures; this catches exit-code-only failures.
			if ex.ExitCode != nil && *ex.ExitCode != 0 {
				if _, hasStatus := compliance["compliance_status"]; !hasStatus {
					compliance["compliance_status"] = "ERROR"
				}
			}
		}

		results = append(results, map[string]any{
			"id":          ex.ID,
			"instance_id": ex.InstanceID,
			"account_id":  ex.AccountID,
			"region":      ex.Region,
			"status":      st,
			"exit_code":   ex.ExitCode,
			"compliance":  compliance,
			"start_time":  ex.StartTime,
			"end_time":    ex.EndTime,
		})
	}

	inProgress := counts["pending"] + counts["running"]
	batchStatus := "running"
	if inProgress == 0 {
		if counts["failed"] > 0 {
			batchStatus = "completed_with_errors"
		} else {
			batchStatus = "completed"
		}
	}

	jsonOK(w, map[string]any{
		"batch_id":      batch.ID,
		"status":        batchStatus,
		"status_counts": counts,
		"results":       results,
	})
}

// handleRHSADownload streams compliance results as a CSV download.
//
// Route: GET /aws/rhsa-compliance/download-results/{batch_id}?format=csv|json|text
func (s *Server) handleRHSADownload(w http.ResponseWriter, r *http.Request) {
	sess := middleware.GetSession(r)
	batch := s.loadToolBatch(w, r.PathValue("batch_id"), sess.AWSAccessKeyID)
	if batch == nil {
		return
	}

	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if format == "" {
		format = "csv"
	}

	batchIDStr := strconv.FormatUint(uint64(batch.ID), 10)

	switch format {
	case "csv":
		var buf bytes.Buffer
		cw := csv.NewWriter(&buf)
		cw.Write([]string{"instance_id", "account_id", "region", "exec_status", "compliance", "hostname", "pkg_mgr", "applied", "missing", "na", "missing_advisories"}) //nolint:errcheck
		for _, ex := range batch.Executions {
			c := parseComplianceOutput(ex.Output)
			missing := ""
			if ids, ok := c["missing_advisories"].([]string); ok {
				missing = strings.Join(ids, "; ")
			}
			cw.Write([]string{ //nolint:errcheck
				ex.InstanceID, ex.AccountID, ex.Region, string(ex.Status),
				fmt.Sprintf("%v", c["compliance_status"]),
				fmt.Sprintf("%v", c["hostname"]),
				fmt.Sprintf("%v", c["pkg_mgr"]),
				fmt.Sprintf("%v", c["applied"]),
				fmt.Sprintf("%v", c["missing"]),
				fmt.Sprintf("%v", c["na"]),
				missing,
			})
		}
		cw.Flush()
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="rhsa_compliance_`+batchIDStr+`.csv"`)
		w.Write(buf.Bytes()) //nolint:errcheck

	case "json":
		type row struct {
			InstanceID string         `json:"instance_id"`
			AccountID  string         `json:"account_id"`
			Region     string         `json:"region"`
			Status     string         `json:"status"`
			Compliance map[string]any `json:"compliance"`
		}
		rows := make([]row, 0, len(batch.Executions))
		for _, ex := range batch.Executions {
			rows = append(rows, row{
				InstanceID: ex.InstanceID,
				AccountID:  ex.AccountID,
				Region:     ex.Region,
				Status:     string(ex.Status),
				Compliance: parseComplianceOutput(ex.Output),
			})
		}
		b, err := json.MarshalIndent(map[string]any{"batch_id": batchIDStr, "results": rows}, "", "  ")
		if err != nil {
			jsonError(w, "failed to marshal results", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", `attachment; filename="rhsa_compliance_`+batchIDStr+`.json"`)
		w.Write(b) //nolint:errcheck

	case "text":
		var buf bytes.Buffer
		for _, ex := range batch.Executions {
			fmt.Fprintf(&buf, "=== Instance: %s | Status: %s ===\n", ex.InstanceID, ex.Status)
			if ex.Output != "" {
				buf.WriteString(ex.Output)
				buf.WriteByte('\n')
			}
			if ex.Error != "" {
				fmt.Fprintf(&buf, "--- stderr ---\n%s\n", ex.Error)
			}
			buf.WriteByte('\n')
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="rhsa_compliance_`+batchIDStr+`.txt"`)
		w.Write(buf.Bytes()) //nolint:errcheck

	default:
		jsonError(w, fmt.Sprintf("unsupported format %q: must be csv, json, or text", format), http.StatusBadRequest)
	}
}

// ── Decom Survey ──────────────────────────────────────────────────────────────

// handleDecomSurvey returns 501 for all decom-survey endpoints.
// The decom survey requires EC2 API data that is not yet wired to the frontend.
func handleDecomSurvey(w http.ResponseWriter, _ *http.Request) {
	jsonError(w, "decom-survey is not yet implemented", http.StatusNotImplemented)
}

// ── Route registration ────────────────────────────────────────────────────────

// registerToolCompatRoutes wires all tool-specific compat endpoints.
// Called from registerRoutes().
func (s *Server) registerToolCompatRoutes(execRL, readRL rateLimiterWrapper) {
	// ── Linux QC Prep ────────────────────────────────────────────────────────
	s.mux.Handle("POST /aws/linux-qc-prep/test-connectivity",
		execRL.Wrap(http.HandlerFunc(s.handleTestConnectivity)))
	s.mux.Handle("POST /aws/linux-qc-prep/execute-qc-step",
		execRL.Wrap(s.requireAWSSession(http.HandlerFunc(s.handleQCStep))))
	s.mux.Handle("GET /aws/linux-qc-prep/qc-results/{batch_id}",
		readRL.Wrap(s.requireAWSSession(http.HandlerFunc(s.handleQCResults))))
	s.mux.Handle("GET /aws/linux-qc-prep/latest-step1-results",
		readRL.Wrap(s.requireAWSSession(http.HandlerFunc(s.handleQCLatestStep1Results))))
	// Download endpoints: stub (501) — LinuxPatcher report rendering not ported.
	s.mux.Handle("GET /aws/linux-qc-prep/download-reports",
		readRL.Wrap(s.requireAWSSession(http.HandlerFunc(handleQCDownload))))
	s.mux.Handle("GET /aws/linux-qc-prep/download-final-report",
		readRL.Wrap(s.requireAWSSession(http.HandlerFunc(handleQCDownload))))

	// ── Linux QC Post ────────────────────────────────────────────────────────
	s.mux.Handle("POST /aws/linux-qc-post/test-connectivity",
		execRL.Wrap(http.HandlerFunc(s.handleTestConnectivity)))
	s.mux.Handle("POST /aws/linux-qc-post/execute-post-validation",
		execRL.Wrap(s.requireAWSSession(http.HandlerFunc(s.handleLinuxQCPostExec))))
	s.mux.Handle("GET /aws/linux-qc-post/validation-results/{batch_id}",
		readRL.Wrap(s.requireAWSSession(http.HandlerFunc(s.handleLinuxQCPostResults))))

	// ── SFT Fixer ────────────────────────────────────────────────────────────
	s.mux.Handle("POST /aws/sft-fixer/validate-instance",
		execRL.Wrap(s.requireAWSSession(http.HandlerFunc(s.handleSFTValidateInstance))))
	s.mux.Handle("POST /aws/sft-fixer/execute-script",
		execRL.Wrap(s.requireAWSSession(http.HandlerFunc(s.handleSFTExecScript))))
	s.mux.Handle("GET /aws/sft-fixer/batch-status/{batch_id}",
		readRL.Wrap(s.requireAWSSession(http.HandlerFunc(s.handleSFTBatchStatus))))

	// ── Disk Recon ───────────────────────────────────────────────────────────
	// Disk-recon uses SSM directly (no batch record); results are polled by
	// command_id rather than batch_id.
	s.mux.Handle("POST /aws/disk-recon/run",
		execRL.Wrap(s.requireAWSSession(http.HandlerFunc(s.handleDiskReconRun))))
	s.mux.Handle("GET /aws/disk-recon/poll/{command_id}",
		readRL.Wrap(s.requireAWSSession(http.HandlerFunc(s.handleDiskReconPoll))))

	// ── RHSA Compliance ──────────────────────────────────────────────────────
	s.mux.Handle("POST /aws/rhsa-compliance/test-connectivity",
		execRL.Wrap(http.HandlerFunc(s.handleTestConnectivity)))
	s.mux.Handle("POST /aws/rhsa-compliance/execute",
		execRL.Wrap(s.requireAWSSession(http.HandlerFunc(s.handleRHSAExecute))))
	s.mux.Handle("GET /aws/rhsa-compliance/results/{batch_id}",
		readRL.Wrap(s.requireAWSSession(http.HandlerFunc(s.handleRHSAResults))))
	s.mux.Handle("GET /aws/rhsa-compliance/download-results/{batch_id}",
		readRL.Wrap(s.requireAWSSession(http.HandlerFunc(s.handleRHSADownload))))

	// ── Decom Survey ─────────────────────────────────────────────────────────
	s.mux.HandleFunc("POST /aws/decom-survey/scan", handleDecomSurvey)
	s.mux.HandleFunc("GET /aws/decom-survey/results/{batch_id}", handleDecomSurvey)
	s.mux.HandleFunc("GET /aws/decom-survey/download", handleDecomSurvey)
}
