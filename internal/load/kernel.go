package load

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/noesis-sol/orchestra/internal/command"
)

// Pressure is the kernel's memory pressure: whether memory is short, which memory used against total
// doesn't say (macOS keeps spare memory full of cache and compressed pages).
type Pressure int

// The pressure levels, as macOS names them.
const (
	PressureUnknown Pressure = iota
	PressureNormal
	PressureWarn
	PressureCritical
)

// String is the level's name: normal, warn or critical; "" when unknown.
func (p Pressure) String() string {
	switch p {
	case PressureNormal:
		return "normal"
	case PressureWarn:
		return "warn"
	case PressureCritical:
		return "critical"
	}
	return ""
}

// Kernel is what the kernel says of the machine's memory and CPU.
type Kernel struct {
	Pressure Pressure
	Swaps    int64   // pages swapped in and out since the machine started; -1 when not known
	Load1    float64 // the 1-minute load average; -1 when not known
	Cores    int     // 0 when not known
	// Available is the memory available to start programs without swapping, in bytes (Linux's
	// MemAvailable); -1 where the kernel doesn't say, as on macOS.
	Available int64
}

// unknownKernel is a Kernel that says nothing yet.
func unknownKernel() Kernel { return Kernel{Swaps: -1, Load1: -1, Available: -1} }

// ReadKernel reads this machine's Kernel: on macOS from sysctl and vm_stat, on Linux from /proc.
func ReadKernel(ctx context.Context) (Kernel, error) {
	switch runtime.GOOS {
	case "darwin":
		sysctl, err := command.Output(ctx, command.ReadLimit, "", "sysctl", "-n",
			"kern.memorystatus_vm_pressure_level", "vm.loadavg", "hw.ncpu")
		if err != nil {
			return Kernel{}, err
		}
		vmstat, err := command.Output(ctx, command.ReadLimit, "", "vm_stat")
		if err != nil {
			return Kernel{}, err
		}
		return ParseDarwin(sysctl, vmstat)
	case "linux":
		read := func(name string) string {
			b, _ := os.ReadFile(name) // a file the kernel lacks (no pressure stall information) says nothing
			return string(b)
		}
		cores := HostCores(read("/sys/devices/system/cpu/online"), read("/proc/cpuinfo"), runtime.NumCPU())
		return ParseLinux(read("/proc/loadavg"), read("/proc/pressure/memory"), read("/proc/meminfo"),
			read("/proc/vmstat"), cores)
	}
	return Kernel{}, errors.ErrUnsupported
}

// ParseDarwin reads macOS's figures: sysctl -n's answer for kern.memorystatus_vm_pressure_level,
// vm.loadavg and hw.ncpu, a line each ("1", "{ 9.80 8.12 7.40 }", "10"), and vm_stat's page counts.
func ParseDarwin(sysctl, vmstat string) (Kernel, error) {
	k := unknownKernel()
	f := strings.Split(strings.TrimSpace(sysctl), "\n")
	if len(f) != 3 {
		return k, fmt.Errorf("unexpected sysctl output: %q", sysctl)
	}
	switch strings.TrimSpace(f[0]) {
	case "1":
		k.Pressure = PressureNormal
	case "2":
		k.Pressure = PressureWarn
	case "4":
		k.Pressure = PressureCritical
	}
	if avg := strings.Fields(strings.Trim(strings.TrimSpace(f[1]), "{}")); len(avg) > 0 {
		if v, err := strconv.ParseFloat(avg[0], 64); err == nil {
			k.Load1 = v
		}
	}
	if n, err := strconv.Atoi(strings.TrimSpace(f[2])); err == nil {
		k.Cores = n
	}
	in, inOK := counter(vmstat, "Swapins:")
	out, outOK := counter(vmstat, "Swapouts:")
	if inOK && outOK {
		k.Swaps = in + out
	}
	return k, nil
}

// ParseLinux reads Linux's figures from /proc/loadavg, /proc/pressure/memory, /proc/meminfo and
// /proc/vmstat, each "" where unreadable, and the CPU count.
func ParseLinux(loadavg, pressure, meminfo, vmstat string, cores int) (Kernel, error) {
	k := unknownKernel()
	k.Cores = cores
	if f := strings.Fields(loadavg); len(f) > 0 {
		if v, err := strconv.ParseFloat(f[0], 64); err == nil {
			k.Load1 = v
		}
	}
	total, totalOK := counter(meminfo, "MemTotal:")
	if avail, ok := counter(meminfo, "MemAvailable:"); ok {
		k.Available = avail * 1024
	}
	some, someOK := stall(pressure, "some")
	full, fullOK := stall(pressure, "full")
	switch {
	case someOK || fullOK:
		k.Pressure = PressureNormal
		if some >= StallWarn {
			k.Pressure = PressureWarn
		}
		if full >= StallCritical {
			k.Pressure = PressureCritical
		}
	case totalOK && total > 0 && k.Available >= 0:
		share := float64(k.Available) / float64(total*1024)
		k.Pressure = PressureNormal
		if share < AvailableWarn {
			k.Pressure = PressureWarn
		}
		if share < AvailableCritical {
			k.Pressure = PressureCritical
		}
	}
	in, inOK := counter(vmstat, "pswpin")
	out, outOK := counter(vmstat, "pswpout")
	if inOK && outOK {
		k.Swaps = in + out
	}
	if k.Load1 < 0 && k.Pressure == PressureUnknown {
		return k, errors.New("nothing readable in /proc")
	}
	return k, nil
}

// HostCores is the count of the host's online CPUs, which /proc/loadavg's load is spread over: from
// /sys/devices/system/cpu/online ("0-3,8-11"), else from /proc/cpuinfo's processor lines, each "" where
// unreadable. affinity, the CPUs this process may run on (runtime.NumCPU), is the answer where neither says
// more: in a container pinned to 4 of a host's 64 cores, the host's load against 4 would read as overload.
func HostCores(online, cpuinfo string, affinity int) int {
	n := cpuList(online)
	if n == 0 {
		for line := range strings.Lines(cpuinfo) {
			if name, _, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(name) == "processor" {
				n++
			}
		}
	}
	return max(n, affinity)
}

// cpuList is the count of CPUs in a kernel CPU list, "0-3,8-11" or "0"; 0 when it isn't one.
func cpuList(list string) int {
	list = strings.TrimSpace(list)
	if list == "" {
		return 0
	}
	n := 0
	for part := range strings.SplitSeq(list, ",") {
		lo, hi, isRange := strings.Cut(part, "-")
		if !isRange {
			hi = lo
		}
		first, err1 := strconv.Atoi(lo)
		last, err2 := strconv.Atoi(hi)
		if err1 != nil || err2 != nil || first < 0 || last < first {
			return 0
		}
		n += last - first + 1
	}
	return n
}

// counter is the number on text's line that starts with name, as vm_stat ("Swapins:  8873329."),
// /proc/meminfo ("MemTotal:  16384 kB") and /proc/vmstat ("pswpin 12") give theirs.
func counter(text, name string) (int64, bool) {
	for line := range strings.Lines(text) {
		rest, ok := strings.CutPrefix(line, name)
		if !ok {
			continue
		}
		f := strings.Fields(rest)
		if len(f) == 0 {
			return 0, false
		}
		n, err := strconv.ParseInt(strings.TrimSuffix(f[0], "."), 10, 64)
		return n, err == nil
	}
	return 0, false
}

// stall is the avg10 on the line of /proc/pressure/memory that starts with kind, some or full:
// "some avg10=1.53 avg60=0.40 avg300=0.10 total=123".
func stall(pressure, kind string) (float64, bool) {
	for line := range strings.Lines(pressure) {
		f := strings.Fields(line)
		if len(f) < 2 || f[0] != kind {
			continue
		}
		v, ok := strings.CutPrefix(f[1], "avg10=")
		if !ok {
			return 0, false
		}
		n, err := strconv.ParseFloat(v, 64)
		return n, err == nil
	}
	return 0, false
}
