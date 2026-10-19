package dispatch

import (
	"bytes"
	"errors"
	"os"
	"strconv"
	"time"
)

// userHz is the unit of /proc's clock ticks: USER_HZ, 100 on every architecture Linux runs Go on.
const userHz = 100

// processStart returns when process pid started: its start in clock ticks since boot, in
// /proc/<pid>/stat, after the boot time in /proc/stat, to the second.
func processStart(pid int) (time.Time, error) {
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return time.Time{}, err
	}
	// The command's name, in parentheses, may hold spaces and parentheses; the fields after it begin
	// with the third, the state, so the start time, the 22nd, is the 20th of them.
	var fields [][]byte
	if i := bytes.LastIndexByte(stat, ')'); i >= 0 {
		fields = bytes.Fields(stat[i+1:])
	}
	if len(fields) < 20 {
		return time.Time{}, errors.New("unexpected /proc/<pid>/stat")
	}
	ticks, err := strconv.ParseInt(string(fields[19]), 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	boot, err := bootTime()
	if err != nil {
		return time.Time{}, err
	}
	return boot.Add(time.Duration(ticks) * time.Second / userHz), nil
}

// bootTime is when the machine booted, btime in /proc/stat.
func bootTime() (time.Time, error) {
	stat, err := os.ReadFile("/proc/stat")
	if err != nil {
		return time.Time{}, err
	}
	for line := range bytes.Lines(stat) {
		if v, ok := bytes.CutPrefix(line, []byte("btime ")); ok {
			s, err := strconv.ParseInt(string(bytes.TrimSpace(v)), 10, 64)
			if err != nil {
				return time.Time{}, err
			}
			return time.Unix(s, 0), nil
		}
	}
	return time.Time{}, errors.New("no btime in /proc/stat")
}
