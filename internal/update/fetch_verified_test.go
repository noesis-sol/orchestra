package update

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"runtime"
	"slices"
	"testing"
)

// assetsOf finds the platform's archive, checksums.txt and its signature, or names the first missing.
func TestAssetsOf(t *testing.T) {
	name := ArchiveName("v0.6.0", "linux", "amd64")
	all := map[string]string{name: "a", Checksums: "s", Signature: "g"}
	without := func(missing string) map[string]string {
		m := map[string]string{}
		for k, v := range all {
			if k != missing {
				m[k] = v
			}
		}
		return m
	}
	for _, tc := range []struct {
		assets   map[string]string
		missing  string // the MissingAssetError's Name, "" for none
		platform string // its Platform
	}{
		{all, "", ""},
		{without(name), name, "linux/amd64"},
		{without(Checksums), Checksums, ""},
		{without(Signature), Signature, ""},
	} {
		u := Updater{GOOS: "linux", GOARCH: "amd64"}
		a, err := u.assetsOf(Release{Tag: "v0.6.0", Assets: tc.assets})
		ma, ok := errors.AsType[*MissingAssetError](err)
		switch {
		case tc.missing == "" && (err != nil || a != releaseAssets{name, "a", "s", "g"}):
			t.Errorf("all there: %+v, %v", a, err)
		case tc.missing != "" && (!ok || ma.Name != tc.missing || ma.Platform != tc.platform || ma.Release != "v0.6.0"):
			t.Errorf("without %s: err = %v, want a *MissingAssetError for it", tc.missing, err)
		}
	}
}

// fetchVerified returns the archive once checksums.txt's signature and the archive's sum check out;
// a signature by another key fails before the archive is downloaded, and a tampered archive or one
// checksums.txt doesn't list fails on its sum.
func TestFetchVerified(t *testing.T) {
	name := ArchiveName("v0.6.0", runtime.GOOS, runtime.GOARCH)
	for _, tc := range []struct {
		about    string
		change   func(r *fakeRelease, u *Updater)
		want     func(error) bool
		archived bool // whether the archive was downloaded
	}{
		{"verified", func(*fakeRelease, *Updater) {}, func(err error) bool { return err == nil }, true},
		{"another key", func(_ *fakeRelease, u *Updater) {
			u.Keys = []ed25519.PublicKey{newKey(t).Public().(ed25519.PublicKey)}
		}, func(err error) bool { _, ok := errors.AsType[*SignatureError](err); return ok }, false},
		{"tampered", func(r *fakeRelease, _ *Updater) {
			r.files[name] = archive(t, "orchestra v0.6.0, tampered with\n")
		}, func(err error) bool { _, ok := errors.AsType[*ChecksumError](err); return ok }, true},
		{"no sum", func(r *fakeRelease, _ *Updater) {
			r.files[Checksums] = []byte("0000  orchestra_x_plan9_386.tar.gz\n")
			r.files[Signature] = ed25519.Sign(r.key, r.files[Checksums])
		}, func(err error) bool {
			return err != nil && err.Error() == Checksums+" has no sum for "+name
		}, false},
	} {
		t.Run(tc.about, func(t *testing.T) {
			r := newRelease(t, "v0.6.0", "orchestra v0.6.0\n")
			want := r.files[name]
			u := r.serve(t)
			tc.change(r, &u)
			a, err := u.assetsOf(latest(t, u))
			if err != nil {
				t.Fatal(err)
			}
			got, err := u.fetchVerified(t.Context(), a, u.Keys)
			if !tc.want(err) {
				t.Errorf("err = %v", err)
			}
			if err == nil && !bytes.Equal(got, want) {
				t.Error("returned another archive than the release's")
			}
			if archived := slices.Contains(r.requested(), "/download/"+name); archived != tc.archived {
				t.Errorf("archive downloaded: %t, want %t", archived, tc.archived)
			}
		})
	}
}
