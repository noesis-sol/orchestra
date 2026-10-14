package project

import (
	"errors"
	"testing"
	"time"
)

func TestParseDuration(t *testing.T) {
	for _, tc := range []struct {
		text   string
		zeroOK bool
		want   time.Duration
		err    string
	}{
		{"5m", false, 5 * time.Minute, ""},
		{"0", false, 0, "must be a positive duration such as 20m (got '0')"},
		{"-5m", false, 0, "must be a positive duration such as 20m (got '-5m')"},
		{"soon", false, 0, "must be a positive duration such as 20m (got 'soon')"},
		{"2h", true, 2 * time.Hour, ""},
		{"0", true, 0, ""},
		{"0s", true, 0, ""},
		{"-1h", true, 0, "must be a duration such as 20m, or 0 for none (got '-1h')"},
		{"", true, 0, "must be a duration such as 20m, or 0 for none (got '')"},
	} {
		got, err := ParseDuration(tc.text, "20m", tc.zeroOK)
		if got != tc.want || errText(err) != tc.err {
			t.Errorf("ParseDuration(%q, %v) = %s, %v", tc.text, tc.zeroOK, got, err)
		}
	}
}

// resolve takes the flag's value when given and checked, else the setting's, else the default.
func TestResolve(t *testing.T) {
	bad := errors.New("bad")
	odd := func(n int) error {
		if n%2 == 0 {
			return bad
		}
		return nil
	}
	settingOf := func(v int, ok bool, err error) setting[int] {
		return func() (int, bool, error) { return v, ok, err }
	}
	for _, tc := range []struct {
		name  string
		flag  Given[int]
		check func(int) error
		read  setting[int]
		want  int
		err   error
	}{
		{"the default", Given[int]{}, odd, settingOf(0, false, nil), 9, nil},
		{"the setting", Given[int]{}, odd, settingOf(4, true, nil), 4, nil}, // not held to the flag's check
		{"a bad setting", Given[int]{}, odd, settingOf(0, false, bad), 0, bad},
		{"the flag", Given[int]{Value: 3, Given: true}, odd, settingOf(4, true, nil), 3, nil},
		{"the flag over a bad setting", Given[int]{Value: 3, Given: true}, odd, settingOf(0, false, bad), 3, nil},
		{"a bad flag", Given[int]{Value: 2, Given: true}, odd, settingOf(4, true, nil), 0, bad},
		{"a flag given its zero", Given[int]{Given: true}, nil, settingOf(4, true, nil), 0, nil},
		{"a value not given", Given[int]{Value: 3}, odd, settingOf(0, false, nil), 9, nil},
	} {
		got, err := resolve(tc.flag, tc.check, tc.read, 9)
		if got != tc.want || !errors.Is(err, tc.err) {
			t.Errorf("%s: %d, %v; want %d, %v", tc.name, got, err, tc.want, tc.err)
		}
	}
}

// Every setting's problem names settings.json and its key, as a run reports it.
func TestSettingProblems(t *testing.T) {
	count := func(n int) *int { return &n }
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"concurrent", second(ResolveConcurrency(Given[int]{}, Settings{Concurrency: -1})),
			".orchestra/settings.json: concurrent must be between 1 and 16 (got -1)"},
		{"check_hand_backs", second(ResolveCheckHandBacks(Settings{CheckHandBacks: count(-1)})),
			".orchestra/settings.json: check_hand_backs must be 0 (never) or more (got -1)"},
		{"environment_hold count", third(ResolveEnvironmentHold(Settings{EnvironmentHold: &EnvironmentHold{Count: count(-2)}})),
			".orchestra/settings.json: environment_hold count must be 0 (off) or more (got -2)"},
		{"environment_hold window", third(ResolveEnvironmentHold(Settings{EnvironmentHold: &EnvironmentHold{Window: "0"}})),
			".orchestra/settings.json: environment_hold window must be a positive duration such as 2m (got '0')"},
		{"environment_hold probe", second(ResolveEnvironmentProbe(Settings{EnvironmentHold: &EnvironmentHold{Probe: "-1m"}})),
			".orchestra/settings.json: environment_hold probe must be a duration such as 10m, or 0 for none (got '-1m')"},
		{"terminal", second(ResolveTerminal("", Settings{Terminal: "screen"}, terminals)),
			".orchestra/settings.json: terminal must be one of herdr, tmux, cmux, auto (got 'screen')"},
		{"--terminal", second(ResolveTerminal("screen", Settings{}, terminals)),
			"--terminal (or ORCHESTRA_TERMINAL) must be one of herdr, tmux, cmux, auto (got 'screen')"},
		{"setup_files", second(ResolveSetupFiles(Settings{SetupFiles: &[]string{"["}})),
			`.orchestra/settings.json: setup_files must be a list of file names or globs such as ["package-lock.json"] ` +
				`(got "[")`},
	} {
		if errText(tc.err) != tc.want {
			t.Errorf("%s: %v\nwant %s", tc.name, tc.err, tc.want)
		}
	}
}

func second[T any](_ T, err error) error { return err }

func third[A, B any](_ A, _ B, err error) error { return err }

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
