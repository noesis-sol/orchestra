package update

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// CacheDir reads the environment it is given, not the process's: orchestra's folder in the user's
// cache folder as os.UserCacheDir finds it, and an error where that has none.
func TestCacheDirReadsTheEnvironmentGiven(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the cache folder is %LocalAppData%'s")
	}
	home, xdg := "/home/u", "/var/cache/u"
	want := map[bool]string{true: xdg + "/orchestra", false: home + "/.cache/orchestra"} // by XDG_CACHE_HOME set
	if runtime.GOOS == "darwin" || runtime.GOOS == "ios" {
		want = map[bool]string{true: home + "/Library/Caches/orchestra", false: home + "/Library/Caches/orchestra"}
	}
	for _, tc := range []struct {
		name string
		env  map[string]string
		want string // "" for an error
	}{
		{"HOME", map[string]string{"HOME": home}, want[false]},
		{"XDG_CACHE_HOME too", map[string]string{"HOME": home, "XDG_CACHE_HOME": xdg}, want[true]},
		{"neither", map[string]string{}, ""},
	} {
		dir, err := CacheDir(func(k string) string { return tc.env[k] })
		if dir != tc.want || (err != nil) != (tc.want == "") {
			t.Errorf("%s: %q, %v; want %q", tc.name, dir, err, tc.want)
		}
	}
	user, err := os.UserCacheDir()
	if err == nil {
		user = filepath.Join(user, "orchestra")
	}
	if dir, got := CacheDir(os.Getenv); dir != user || (got != nil) != (err != nil) {
		t.Errorf("the process's environment: %q, %v; os.UserCacheDir has %q, %v", dir, got, user, err)
	}
}
