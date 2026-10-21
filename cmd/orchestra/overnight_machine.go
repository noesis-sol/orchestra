package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/noesis-sol/orchestra/internal/command"
)

// What the overnight pre-flight's warnings read of the machine beside the kernel's figures
// (internal/load): its processes by name, the virtual machines among them, and whether it runs on
// battery.

// namedProcess is one process of a listing, with its name: what it runs, the base name of its
// executable.
type namedProcess struct {
	pid, parent int
	memory      int64   // resident, in bytes
	cpu         float64 // the share of a core it has used lately, in percent, as ps gives it
	name        string
}

// listNamed lists the machine's processes with their names, with ps, the same on macOS and Linux.
func listNamed(ctx context.Context) ([]namedProcess, error) {
	out, err := command.Output(ctx, command.ReadLimit, "", "ps", "-A", "-o", "pid=,ppid=,rss=,pcpu=,comm=")
	if err != nil {
		return nil, err
	}
	return parseNamed(out)
}

// parseNamed reads ps's listing of each process's ID, its parent's, its resident memory in kilobytes,
// its CPU share and its command, a process a line: macOS gives the command as the executable's path,
// spaces and all, Linux its name.
func parseNamed(out string) ([]namedProcess, error) {
	var ps []namedProcess
	for line := range strings.Lines(out) {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		bad := fmt.Errorf("unexpected line in the process listing: %q", line)
		if len(f) < 5 {
			return nil, bad
		}
		var n [3]int64
		for i := range n {
			v, err := strconv.ParseInt(f[i], 10, 64)
			if err != nil {
				return nil, bad
			}
			n[i] = v
		}
		cpu, err := strconv.ParseFloat(strings.ReplaceAll(f[3], ",", "."), 64) // a locale's decimal comma
		if err != nil {
			return nil, bad
		}
		name := strings.Join(f[4:], " ")
		if strings.HasPrefix(name, "/") { // macOS's path; a Linux name may hold a slash (kworker/0:1)
			name = path.Base(name)
		}
		ps = append(ps, namedProcess{pid: int(n[0]), parent: int(n[1]), memory: n[2] * 1024, cpu: cpu, name: name})
	}
	return ps, nil
}

// heaviest names the n processes of ps that weigh most by weight, heaviest first, those weighing
// nothing left out, each as said says it.
func heaviest(ps []namedProcess, n int, weight func(namedProcess) float64,
	said func(namedProcess) string) []string {
	ps = slices.Clone(ps)
	slices.SortStableFunc(ps, func(a, b namedProcess) int {
		switch wa, wb := weight(a), weight(b); {
		case wa > wb:
			return -1
		case wa < wb:
			return 1
		}
		return 0
	})
	var names []string
	for _, p := range ps[:min(n, len(ps))] {
		if weight(p) > 0 {
			names = append(names, said(p))
		}
	}
	return names
}

// vmMemory is the memory a virtual machine holds past which the pre-flight warns of it: the workers
// and their checks get what it leaves.
const vmMemory = 4 * gb

// vmProcess reports whether the process name runs a virtual machine: Apple's Virtualization framework,
// which Docker Desktop, OrbStack, Colima and Podman use on macOS, or the hypervisors before it, QEMU
// and the desktop ones.
func vmProcess(name string) bool {
	switch name {
	case "com.apple.Virtualization.VirtualMachine", "com.docker.hyperkit", "hyperkit", "vfkit", "krunkit",
		"VBoxHeadless", "VirtualBoxVM", "vmware-vmx", "prl_vm_app":
		return true
	}
	return strings.HasPrefix(name, "qemu-system-")
}

// vmApps are the applications that run virtual machines, by the name of a process of theirs, for
// whose a large virtual machine is: Virtualization's machines run under launchd, not their app.
var vmApps = map[string]string{
	"com.docker.backend": "Docker Desktop", "com.docker.virtualization": "Docker Desktop",
	"com.docker.hyperkit": "Docker Desktop", "OrbStack": "OrbStack", "OrbStack Helper": "OrbStack",
	"limactl": "Lima (Colima)", "colima": "Colima", "podman": "Podman", "gvproxy": "Podman", "UTM": "UTM",
}

// largeVMs says each virtual machine among ps holding more than vmMemory, and whose it is where the
// listing tells: the application above it, else those of vmApps running.
func largeVMs(ps []namedProcess) []string {
	byPID := map[int]namedProcess{}
	var running []string
	for _, p := range ps {
		byPID[p.pid] = p
		if app, ok := vmApps[p.name]; ok && !slices.Contains(running, app) {
			running = append(running, app)
		}
	}
	slices.Sort(running)
	var vms []string
	for _, p := range ps {
		if !vmProcess(p.name) || p.memory <= vmMemory {
			continue
		}
		whose := "a virtual machine"
		for up, seen := p.parent, 0; up > 1 && seen < 32; up, seen = byPID[up].parent, seen+1 {
			if app, ok := vmApps[byPID[up].name]; ok {
				whose = app + "'s virtual machine"
				break
			}
		}
		vm := fmt.Sprintf("%s holds %s of memory (%s, PID %d)", whose, memoryGB(p.memory), p.name, p.pid)
		if whose == "a virtual machine" && len(running) > 0 {
			vm += fmt.Sprintf("; %s %s running", joinAnd(running), isAre(len(running)))
		}
		vms = append(vms, vm)
	}
	return vms
}

// memoryGB is n bytes in GB, to a tenth under 10.
func memoryGB(n int64) string {
	if n < 10*gb {
		return fmt.Sprintf("%.1f GB", float64(n)/gb)
	}
	return fmt.Sprintf("%d GB", n/gb)
}

// isAre is "is" for one, "are" for more.
func isAre(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

// joinAnd joins items as "a", "a and b", "a, b and c".
func joinAnd(items []string) string {
	if len(items) < 2 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

// errNoPowerSource is the error where the system doesn't say what powers the machine.
var errNoPowerSource = errors.New("the system doesn't say what powers the machine")

// onBattery reports whether the machine runs on battery now: on macOS from pmset, on Linux from the
// power supplies in sysfs; errors.ErrUnsupported elsewhere.
func onBattery(ctx context.Context, goos string) (bool, error) {
	switch goos {
	case "darwin":
		out, err := command.Output(ctx, command.ReadLimit, "", "pmset", "-g", "batt")
		if err != nil {
			return false, err
		}
		return pmsetOnBattery(out)
	case "linux":
		return sysfsOnBattery(os.DirFS("/sys/class/power_supply"))
	}
	return false, errors.ErrUnsupported
}

// pmsetOnBattery reads pmset -g batt's first line: "Now drawing from 'Battery Power'", or 'AC Power',
// or 'UPS Power'.
func pmsetOnBattery(out string) (bool, error) {
	first, _, _ := strings.Cut(out, "\n")
	_, from, ok := strings.Cut(first, "drawing from '")
	if !ok {
		return false, errNoPowerSource
	}
	from, _, _ = strings.Cut(from, "'")
	return from == "Battery Power", nil
}

// sysfsOnBattery reads Linux's power supplies, a folder each in supplies (/sys/class/power_supply):
// the machine runs on battery where no mains supply is online and a battery is discharging. One with
// no supplies at all, as a desktop or a virtual machine, runs on mains.
func sysfsOnBattery(supplies fs.FS) (bool, error) {
	entries, err := fs.ReadDir(supplies, ".")
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	read := func(dir, name string) string {
		b, _ := fs.ReadFile(supplies, dir+"/"+name) // a file a supply lacks says nothing
		return strings.TrimSpace(string(b))
	}
	mains, discharging := false, false
	for _, e := range entries {
		switch read(e.Name(), "type") {
		case "Mains", "USB":
			mains = mains || read(e.Name(), "online") == "1"
		case "Battery":
			discharging = discharging || read(e.Name(), "status") == "Discharging"
		}
	}
	return discharging && !mains, nil
}
