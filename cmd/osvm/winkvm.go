package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// kvmOpts drive image-build-windows-kvm: Windows is installed unattended by a
// QEMU process on THIS machine (a Linux CI runner with /dev/kvm), so the whole
// build sees every disk the way the firmware expects, and only the finished
// disk goes to Glance.
type kvmOpts struct {
	Name, ISO, ISOSHA, VirtioISO, SevenZip, WorkDir, Out string
	ScreenDir                                            string
	Qemu, QemuImg, OVMFCode, OVMFVars, Edition           string
	CPUs, MemoryMB, DiskGB                               int
	Timeout                                              time.Duration
	Upload, DryRun, NoKVM                                bool
}

func (o kvmOpts) validate() error {
	switch {
	case o.ISO == "":
		return errNoISO
	case o.Out == "":
		return errors.New("--out is required: the qcow2 the build leaves")
	case o.Edition == "":
		return errors.New("--edition must not be empty")
	case o.CPUs < 1 || o.MemoryMB < 4096 || o.DiskGB < 40:
		return errors.New("--cpus must be at least 1, --memory-mb at least 4096 and --disk-gb at least 40")
	case o.Timeout <= 0:
		return errors.New("--timeout must be positive")
	}
	return nil
}

// ovmfCodeCandidates and ovmfVarsCandidates are where distributions ship the
// 4 MiB OVMF build (Debian and Ubuntu, Fedora).
var (
	ovmfCodeCandidates = []string{"/usr/share/OVMF/OVMF_CODE_4M.fd", "/usr/share/OVMF/OVMF_CODE.fd", "/usr/share/edk2/ovmf/OVMF_CODE.fd", "/usr/share/qemu/OVMF_CODE.fd"}
	ovmfVarsCandidates = []string{"/usr/share/OVMF/OVMF_VARS_4M.fd", "/usr/share/OVMF/OVMF_VARS.fd", "/usr/share/edk2/ovmf/OVMF_VARS.fd", "/usr/share/qemu/OVMF_VARS.fd"}
)

func firstExisting(given string, candidates []string) (string, error) {
	if given != "" {
		if _, err := os.Stat(given); err != nil {
			return "", err
		}
		return given, nil
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", fmt.Errorf("none of %s exists: install ovmf or pass the path", strings.Join(candidates, ", "))
}

func imageBuildWindowsKVMCmd(cloud *string) *cobra.Command {
	o := kvmOpts{}
	c := &cobra.Command{
		Use:   "image-build-windows-kvm",
		Short: "install Windows unattended under QEMU/KVM on this machine, install the CI tools and cloudbase-init, sysprep, leave a qcow2 (and upload it to Glance with --upload)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if o.Name == "" {
				o.Name = "garm-runner-windows-" + now().UTC().Format("20060102-1504")
			}
			if err := o.validate(); err != nil {
				return err
			}
			if o.Upload && *cloud == "" && !o.DryRun {
				return errors.New("--upload needs --cloud")
			}
			return imageBuildWindowsKVM(cmd.Context(), *cloud, o, cmd.OutOrStdout())
		},
	}
	f := c.Flags()
	f.StringVar(&o.Name, "name", "", "name of the Glance image with --upload (default garm-runner-windows-<UTC date-time>)")
	f.StringVar(&o.ISO, "iso", "", "local Windows 11 x64 installer ISO (a licence you hold; the system stays unactivated)")
	f.StringVar(&o.ISOSHA, "iso-sha256", "", "expected SHA-256 of --iso (refuse to build from a different file)")
	f.StringVar(&o.VirtioISO, "virtio-iso", "", "virtio-win driver ISO attached as a CD; its Windows 11 amd64 drivers are staged in the driver store (optional)")
	f.StringVar(&o.SevenZip, "seven-zip", "7zz", "7-Zip binary that reads the ISO's UDF file system")
	f.StringVar(&o.WorkDir, "work-dir", "", "directory for the boot disk, the root disk and the console log (default <user config dir>/osvm/windows-build)")
	f.StringVar(&o.ScreenDir, "screenshot-dir", "", "directory that receives a PNG of the guest display every 5 minutes and when the build ends (to see where an install hangs)")
	f.StringVar(&o.Out, "out", "", "path of the finished qcow2 (required; must not exist)")
	f.StringVar(&o.Qemu, "qemu", "qemu-system-x86_64", "QEMU system emulator")
	f.StringVar(&o.QemuImg, "qemu-img", "qemu-img", "qemu-img binary")
	f.StringVar(&o.OVMFCode, "ovmf-code", "", "OVMF_CODE firmware (default: the first one found in the usual places)")
	f.StringVar(&o.OVMFVars, "ovmf-vars", "", "OVMF_VARS template (default: the first one found in the usual places)")
	f.StringVar(&o.Edition, "edition", "Windows 11 Pro", "install.wim image name to install")
	f.IntVar(&o.CPUs, "cpus", 4, "guest vCPUs")
	f.IntVar(&o.MemoryMB, "memory-mb", 6144, "guest RAM in MiB")
	f.IntVar(&o.DiskGB, "disk-gb", 64, "virtual size of the Windows disk in GB")
	f.DurationVar(&o.Timeout, "timeout", 3*time.Hour, "bound on the whole build")
	f.BoolVar(&o.Upload, "upload", false, "upload the finished qcow2 to Glance as --name (needs --cloud)")
	f.BoolVar(&o.NoKVM, "no-kvm", false, "emulate without /dev/kvm (hours slower: for a test of the plumbing only)")
	f.BoolVar(&o.DryRun, "dry-run", false, "print the QEMU command line and the answer file (password masked), run nothing")
	return c
}

// kvmPaths are the files one build uses.
type kvmPaths struct{ root, answer, vars, code, console, iso, virtio, monitor, screens string }

// monitorCommand sends one command to the QEMU monitor on sock and gives QEMU
// settle to run it before the connection closes.
func monitorCommand(sock, line string, settle time.Duration) error {
	c, err := net.DialTimeout("unix", sock, 5*time.Second)
	if err != nil {
		return err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(15 * time.Second))
	if _, err := fmt.Fprintf(c, "%s\n", line); err != nil {
		return err
	}
	buf := make([]byte, 4096)
	_, _ = c.Read(buf) // banner; the command runs before the connection closes
	time.Sleep(settle)
	return nil
}

// screenshot asks the QEMU monitor on sock for a PNG of the guest display.
func screenshot(sock, file string) error {
	return monitorCommand(sock, "screendump "+file+" -f png", 2*time.Second)
}

// pressAnyKey answers "Press any key to boot from CD or DVD...": the Windows
// ISO's UEFI boot image (efisys.bin) waits for a key for five seconds and then
// gives up, and nothing is at the keyboard. Enter is sent every few seconds
// for keyFor, longer than firmware start-up plus that prompt; once Windows is
// installed the disk boots first and the CD is never asked. (The alternative,
// remastering the 5 GB ISO with efisys_noprompt.bin, costs a full copy of it.)
func pressAnyKey(ctx context.Context, sock string, keyFor time.Duration) {
	end := time.Now().Add(keyFor)
	for time.Now().Before(end) {
		_ = monitorCommand(sock, "sendkey ret", 300*time.Millisecond)
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

// stallTicks is how many 10 s ticks the Windows disk may stay the same size
// before the build fails: run 45969309942 sat 100 minutes in the UEFI shell with
// a 92 MiB disk. Setup copies GBs within minutes; the later phases (sysprep,
// updates off) grow it too, so 25 minutes of nothing is a dead guest.
const stallTicks = 150

// Console silence bounds, in 10 s ticks. Run 46031301930 sat 40 minutes at a
// black desktop with not one "OSVM:" line while the disk grew a few MiB, so the
// disk-size stall never fired. The answer file writes "OSVM: specialize pass"
// (long before the desktop) and the first-logon command "OSVM: firstlogon ...":
// once any line is there, the script must say "provision start" within
// provisionTicks (25 min, the time a desktop gets after specialize and the
// first logon); with no line at all the guest has no working console by
// noConsoleTicks (80 min, Setup took 55 in the longest run).
const (
	provisionTicks = 150
	noConsoleTicks = 480
	provisionLine  = "provision start"
)

// consoleSilence returns why the build must fail, or "" while it may go on:
// ticks is the current tick, first the tick of the first "OSVM:" line (0 =
// none yet) and started whether the provisioning script said it started.
func consoleSilence(ticks, first int, started bool) string {
	switch {
	case started:
		return ""
	case first == 0 && ticks >= noConsoleTicks:
		return fmt.Sprintf("no \"OSVM:\" line on the guest console after %d minutes: the answer file's commands do not run or COM1 does not reach the host", noConsoleTicks/6)
	case first != 0 && ticks-first >= provisionTicks:
		return fmt.Sprintf("no %q line %d minutes after the first \"OSVM:\" line: the first-logon command did not start the provisioning script", provisionLine, provisionTicks/6)
	}
	return ""
}

// shotDue says whether tick n (10 s each) takes a screenshot: every 20 s for
// the first 500 s, where the CD prompt, Setup's start and its first
// reboot happen, then every 5 minutes.
func shotDue(n int) bool {
	if n <= 50 {
		return n%2 == 0
	}
	return n%30 == 0
}

// diskBytes is the size of the Windows disk file.
func diskBytes(path string) (int64, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	return fi.Size(), true
}

// kvmMinRAMMB is Windows 11's 4 GB minimum, recorded as Glance min_ram.
const kvmMinRAMMB = 4096

// newPassword and answerISO are replaced by tests that make them fail.
var (
	newPassword = randomPassword
	answerISO   = writeAnswerISO
)

// writeAnswerISO writes the answer ISO (autounattend.xml and the provisioning
// script) with a fresh random Administrator password that lives only there.
func writeAnswerISO(path, edition string, w io.Writer) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists", path)
	}
	pw, err := newPassword(24)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "building answer ISO %s\n", path)
	files := map[string][]byte{"autounattend.xml": []byte(unattendXML(edition, pw)), provisionName: []byte(provisionScript)}
	if err := buildAnswerISO(path, files); err != nil {
		os.Remove(path)
		return err
	}
	return nil
}

// keyFor is how long pressAnyKey keeps sending Enter. It must END before Setup
// has a window: run 45897281319 sent Enter for 3 minutes, which reached Setup's
// "Installing Windows 11" screen, pressed its focused Cancel button, and left
// "Are you sure you want to quit?" open at 12% for the rest of the build. The
// firmware reaches the CD prompt (which lasts 5 s) well within this.
const keyFor = 45 * time.Second

// snapshot writes one numbered screenshot and says how big the Windows disk
// has grown, the cheapest sign that Setup is copying files.
func snapshot(p kvmPaths, n int, w io.Writer) {
	if fi, err := os.Stat(p.root); err == nil {
		fmt.Fprintf(w, "disk: %d MiB allocated\n", fi.Size()>>20)
	}
	if p.monitor == "" || p.screens == "" {
		return
	}
	f := filepath.Join(p.screens, fmt.Sprintf("screen-%03d.png", n))
	if err := screenshot(p.monitor, f); err != nil {
		fmt.Fprintln(w, "screenshot:", err)
		return
	}
	fmt.Fprintln(w, "screenshot:", f)
}

// qemuArgs is the whole QEMU command line. The Windows disk is the ONLY fixed
// disk (so Setup's DiskID 0 is it) and the first boot device; it is blank, so
// the firmware falls through to the Windows installer ISO attached as a CD (the
// second boot device, the standard way to install Windows under QEMU), and
// after the install it boots the disk. The answer ISO and the driver ISO are
// further CDs on the same AHCI controller; Setup finds Autounattend.xml on the
// root of a CD and install.wim on its own boot media. The guest console (COM1)
// goes to a file the host reads. -vmx hides VMX from the guest so Windows does
// not try to turn on VBS/Hyper-V on top of the runner's nested KVM.
func qemuArgs(o kvmOpts, p kvmPaths) []string {
	accel := "kvm"
	cpu := "host,-vmx"
	if o.NoKVM {
		accel, cpu = "tcg", "qemu64"
	}
	a := []string{
		"-machine", "q35,accel=" + accel, "-cpu", cpu,
		"-smp", fmt.Sprint(o.CPUs), "-m", fmt.Sprint(o.MemoryMB),
		"-display", "none", "-serial", "file:" + p.console,
		"-drive", "if=pflash,format=raw,readonly=on,file=" + p.code,
		"-drive", "if=pflash,format=raw,file=" + p.vars,
		"-device", "ich9-ahci,id=ahci",
		"-drive", "if=none,id=root,format=qcow2,file=" + p.root,
		"-device", "ide-hd,drive=root,bus=ahci.0,bootindex=1",
		"-drive", "if=none,id=iso,format=raw,media=cdrom,readonly=on,file=" + p.iso,
		"-device", "ide-cd,drive=iso,bus=ahci.1,bootindex=2",
		"-drive", "if=none,id=answer,format=raw,media=cdrom,readonly=on,file=" + p.answer,
		"-device", "ide-cd,drive=answer,bus=ahci.2",
	}
	if p.monitor != "" {
		a = append(a, "-monitor", "unix:"+p.monitor+",server,nowait")
	} else {
		a = append(a, "-monitor", "none")
	}
	if p.virtio != "" {
		a = append(a, "-drive", "if=none,id=virtio,format=raw,media=cdrom,readonly=on,file="+p.virtio, "-device", "ide-cd,drive=virtio,bus=ahci.3")
	}
	return append(a,
		"-netdev", "user,id=n0", "-device", "e1000e,netdev=n0",
		"-rtc", "base=utc")
}

// consoleProgress prints the "OSVM: " lines of the guest console that are new,
// and reports the success marker and the first FAILED line.
func consoleProgress(out string, seen map[string]bool, w io.Writer) (marker bool, failure string) {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(strings.ReplaceAll(line, "\r", ""))
		i := strings.Index(line, "OSVM: ")
		if i < 0 {
			continue
		}
		if strings.Contains(line, buildMarker) {
			marker = true
		}
		if strings.Contains(line, "FAILED:") && failure == "" {
			failure = line[i:]
		}
		if seen[line] {
			continue
		}
		seen[line] = true
		fmt.Fprintln(w, "guest:", line[i+len("OSVM: "):])
	}
	return marker, failure
}

func imageBuildWindowsKVM(ctx context.Context, cloud string, o kvmOpts, w io.Writer) error {
	if o.DryRun {
		fmt.Fprint(w, strings.ReplaceAll(unattendXML(o.Edition, "PASSWORD"), "\r", ""))
		fmt.Fprintln(w, o.Qemu, strings.Join(qemuArgs(o, kvmPaths{root: "ROOT.qcow2", answer: "ANSWER.iso", vars: "VARS.fd", code: "CODE.fd", console: "CONSOLE.log", iso: o.ISO, virtio: o.VirtioISO}), " "))
		return nil
	}
	if _, err := os.Stat(o.Out); err == nil {
		return fmt.Errorf("%s already exists", o.Out)
	}
	if !o.NoKVM {
		if f, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0); err != nil {
			return fmt.Errorf("/dev/kvm is not usable (%v): run on a runner with KVM, or pass --no-kvm", err)
		} else {
			f.Close()
		}
	}
	bctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	if o.ISOSHA != "" {
		sum, _, err := hashFile(o.ISO)
		if err != nil {
			return err
		}
		if sum != o.ISOSHA {
			return fmt.Errorf("%s: sha256 %s is not the expected %s", o.ISO, sum, o.ISOSHA)
		}
	}
	code, err := firstExisting(o.OVMFCode, ovmfCodeCandidates)
	if err != nil {
		return err
	}
	varsTpl, err := firstExisting(o.OVMFVars, ovmfVarsCandidates)
	if err != nil {
		return err
	}
	work, err := workDir(winOpts{WorkDir: o.WorkDir})
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp(work, "kvm-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	p := kvmPaths{
		root: filepath.Join(dir, "root.qcow2"), vars: filepath.Join(dir, "vars.fd"), answer: filepath.Join(dir, "answer.iso"),
		code: code, console: filepath.Join(dir, "console.log"), iso: o.ISO, virtio: o.VirtioISO,
	}
	// the monitor socket is always there: it presses the "any key" of the
	// installer CD, and takes the screenshots when asked for
	// (a unix socket path is limited to about 100 bytes: keep it short)
	mdir, err := os.MkdirTemp("", "osvm-m")
	if err != nil {
		return err
	}
	defer os.RemoveAll(mdir)
	p.monitor = filepath.Join(mdir, "m.sock")
	if o.ScreenDir != "" {
		if err := os.MkdirAll(o.ScreenDir, 0o755); err != nil {
			return err
		}
		p.screens = o.ScreenDir
	}
	if err := checkEdition(bctx, o.SevenZip, o.ISO, work, o.Edition, w); err != nil {
		return err
	}
	if err := answerISO(p.answer, o.Edition, w); err != nil {
		return err
	}
	vars, err := os.ReadFile(varsTpl)
	if err != nil {
		return err
	}
	if err := os.WriteFile(p.vars, vars, 0o600); err != nil {
		return err
	}
	if out, err := exec.CommandContext(bctx, o.QemuImg, "create", "-f", "qcow2", p.root, fmt.Sprintf("%dG", o.DiskGB)).CombinedOutput(); err != nil {
		return fmt.Errorf("qemu-img create: %v: %s", err, out)
	}
	if err := os.WriteFile(p.console, nil, 0o600); err != nil {
		return err
	}
	if err := runQemu(bctx, o, p, w); err != nil {
		return err
	}
	fmt.Fprintln(w, "compacting", p.root, "into", o.Out)
	if out, err := exec.CommandContext(bctx, o.QemuImg, "convert", "-O", "qcow2", p.root, o.Out).CombinedOutput(); err != nil {
		os.Remove(o.Out)
		return fmt.Errorf("qemu-img convert: %v: %s", err, out)
	}
	fmt.Fprintln(w, "image built:", o.Out)
	if !o.Upload {
		return nil
	}
	c, err := connect(bctx, cloud)
	if err != nil {
		return err
	}
	fmt.Fprintln(w, "project:", projectName(c.provider))
	id, err := ensureSourceImage(bctx, c, sourceImage{Name: o.Name, Path: o.Out, Format: "qcow2", Props: windowsImageProps, MinDisk: o.DiskGB, MinRAM: kvmMinRAMMB}, "", w)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "image uploaded: %s id=%s\n", o.Name, id)
	return nil
}

// runQemu runs the guest until it powers itself off (sysprep /shutdown) and
// follows its console. A power-off without the marker, a FAILED line or the
// timeout kills the guest and fails the build: no image is taken from it.
func runQemu(ctx context.Context, o kvmOpts, p kvmPaths, w io.Writer) error {
	cmd := exec.CommandContext(ctx, o.Qemu, qemuArgs(o, p)...)
	cmd.Stdout, cmd.Stderr = nil, os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("qemu: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	if p.monitor != "" {
		kctx, kcancel := context.WithCancel(ctx)
		defer kcancel()
		go pressAnyKey(kctx, p.monitor, keyFor)
	}
	seen := map[string]bool{}
	marker := false
	read := func() string {
		b, _ := os.ReadFile(p.console)
		m, fail := consoleProgress(string(b), seen, w)
		marker = marker || m
		return fail
	}
	ticks, shots := 0, 0
	var lastSize int64
	lastTick, firstLine := 0, 0
	for {
		select {
		case err := <-done:
			fail := read()
			switch {
			case fail != "":
				return fmt.Errorf("the guest reported a failure: %s", fail)
			case err != nil:
				return fmt.Errorf("qemu ended: %w", err)
			case !marker:
				return fmt.Errorf("the guest powered off without %s on its console: no image taken", buildMarker)
			}
			return nil
		case <-ctx.Done():
			snapshot(p, 999, w)
			_ = cmd.Process.Kill()
			<-done
			read()
			return fmt.Errorf("the guest did not finish (marker seen: %t): %w", marker, ctx.Err())
		case <-time.After(10 * time.Second):
			ticks++
			if shotDue(ticks) {
				shots++
				snapshot(p, shots, w)
			}
			if size, ok := diskBytes(p.root); ok {
				if size != lastSize {
					lastSize, lastTick = size, ticks
				} else if ticks-lastTick >= stallTicks {
					snapshot(p, 997, w)
					_ = cmd.Process.Kill()
					<-done
					read()
					return fmt.Errorf("the Windows disk did not grow for %d minutes (stuck at %d MiB): Setup is not installing", stallTicks/6, size>>20)
				}
			}
			if fail := read(); fail != "" {
				snapshot(p, 998, w)
				_ = cmd.Process.Kill()
				<-done
				return fmt.Errorf("the guest reported a failure: %s", fail)
			}
			started := false
			for line := range seen {
				if strings.Contains(line, provisionLine) {
					started = true
				}
			}
			if len(seen) > 0 && firstLine == 0 {
				firstLine = ticks
			}
			if why := consoleSilence(ticks, firstLine, started); why != "" {
				snapshot(p, 996, w)
				_ = cmd.Process.Kill()
				<-done
				return errors.New(why)
			}
		}
	}
}
