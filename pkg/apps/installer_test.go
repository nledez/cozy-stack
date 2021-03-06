package apps

import (
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/cozy/checkup"
	"github.com/cozy/cozy-stack/pkg/config"
	"github.com/cozy/cozy-stack/pkg/consts"
	"github.com/cozy/cozy-stack/pkg/couchdb"
	"github.com/cozy/cozy-stack/pkg/permissions"
	"github.com/cozy/cozy-stack/pkg/vfs"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
)

var localGitCmd *exec.Cmd
var localGitDir string
var localVersion = "1.0.0"
var ts *httptest.Server

type transport struct{}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	req2 := new(http.Request)
	*req2 = *req
	req2.URL, _ = url.Parse(ts.URL)
	return http.DefaultTransport.RoundTrip(req2)
}

func manifest() string {
	return strings.Replace(`{
  "description": "A mini app to test cozy-stack-v2",
  "developer": {
    "name": "Bruno",
    "url": "cozy.io"
  },
  "license": "MIT",
  "name": "mini-app",
  "permissions": {},
  "slug": "mini",
  "version": "`+localVersion+`"
}`, "\n", "", -1)
}

func serveGitRep() {
	dir, err := ioutil.TempDir("", "cozy-app")
	if err != nil {
		panic(err)
	}
	localGitDir = dir
	args := `
echo '` + manifest() + `' > manifest.webapp && \
git init . && \
git add . && \
git commit -m "Initial commit"`
	cmd := exec.Command("sh", "-c", args)
	cmd.Dir = localGitDir
	if err := cmd.Run(); err != nil {
		panic(err)
	}

	// "git daemon --reuseaddr --base-path=./ --export-all ./.git"
	localGitCmd = exec.Command("git", "daemon", "--reuseaddr", "--base-path=./", "--export-all", "./.git")
	localGitCmd.Dir = localGitDir
	if out, err := localGitCmd.CombinedOutput(); err != nil {
		fmt.Println(string(out))
		panic(err)
	}
}

func doUpgrade() {
	localVersion = "2.0.0"
	args := `
echo '` + manifest() + `' > manifest.webapp && \
git commit -am "Upgrade commit"`
	cmd := exec.Command("sh", "-c", args)
	cmd.Dir = localGitDir
	if err := cmd.Run(); err != nil {
		panic(err)
	}
}

type TestContext struct {
	prefix string
	fs     afero.Fs
}

func (c TestContext) Prefix() string { return c.prefix }
func (c TestContext) FS() afero.Fs   { return c.fs }

var c = &TestContext{
	prefix: "apps-test/",
	fs:     afero.NewMemMapFs(),
}

func TestInstallBadSlug(t *testing.T) {
	_, err := NewInstaller(c, &InstallerOptions{
		SourceURL: "git://foo.bar",
	})
	if assert.Error(t, err) {
		assert.Equal(t, ErrInvalidSlugName, err)
	}

	_, err = NewInstaller(c, &InstallerOptions{
		Slug:      "coucou/",
		SourceURL: "git://foo.bar",
	})
	if assert.Error(t, err) {
		assert.Equal(t, ErrInvalidSlugName, err)
	}
}

func TestInstallBadAppsSource(t *testing.T) {
	_, err := NewInstaller(c, &InstallerOptions{
		Slug:      "app2",
		SourceURL: "",
	})
	if assert.Error(t, err) {
		assert.Equal(t, ErrNotSupportedSource, err)
	}

	_, err = NewInstaller(c, &InstallerOptions{
		Slug:      "app3",
		SourceURL: "foo://bar.baz",
	})
	if assert.Error(t, err) {
		assert.Equal(t, ErrNotSupportedSource, err)
	}

	_, err = NewInstaller(c, &InstallerOptions{
		Slug:      "app4",
		SourceURL: "git://bar  .baz",
	})
	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "invalid character")
	}
}

func TestInstallSuccessful(t *testing.T) {
	inst, err := NewInstaller(c, &InstallerOptions{
		Slug:      "local-cozy-mini",
		SourceURL: "git://localhost/",
	})
	if !assert.NoError(t, err) {
		return
	}

	go inst.InstallOrUpdate()

	var state State
	for {
		man, done, err := inst.Poll()
		if !assert.NoError(t, err) {
			return
		}
		if state == "" {
			if !assert.EqualValues(t, Installing, man.State) {
				return
			}
		} else if state == Installing {
			if !assert.EqualValues(t, Ready, man.State) {
				return
			}
			if !assert.True(t, done) {
				return
			}
			break
		} else {
			t.Fatalf("invalid state")
			return
		}
		state = man.State
	}
}

func TestInstallAldreadyExist(t *testing.T) {
	inst, err := NewInstaller(c, &InstallerOptions{
		Slug:      "cozy-app-a",
		SourceURL: "git://localhost/",
	})
	if !assert.NoError(t, err) {
		return
	}

	go inst.InstallOrUpdate()

	for {
		var done bool
		_, done, err = inst.Poll()
		if !assert.NoError(t, err) {
			return
		}
		if done {
			break
		}
	}

	inst, err = NewInstaller(c, &InstallerOptions{
		Slug:      "cozy-app-a",
		SourceURL: "git://localhost/",
	})
	if !assert.NoError(t, err) {
		return
	}

	go inst.InstallOrUpdate()

	man, done, err := inst.Poll()
	if !assert.NoError(t, err) {
		return
	}
	if !assert.EqualValues(t, Ready, man.State) {
		return
	}
	if !assert.True(t, done) {
		return
	}
}

func TestInstallWithUpgrade(t *testing.T) {
	inst, err := NewInstaller(c, &InstallerOptions{
		Slug:      "cozy-app-b",
		SourceURL: "git://localhost/",
	})
	if !assert.NoError(t, err) {
		return
	}

	go inst.InstallOrUpdate()

	for {
		var done bool
		_, done, err = inst.Poll()
		if !assert.NoError(t, err) {
			return
		}
		if done {
			break
		}
	}

	doUpgrade()

	inst, err = NewInstaller(c, &InstallerOptions{
		Slug:      "cozy-app-b",
		SourceURL: "git://localhost/",
	})
	if !assert.NoError(t, err) {
		return
	}

	go inst.InstallOrUpdate()

	var state State
	for {
		man, done, err := inst.Poll()
		if !assert.NoError(t, err) {
			return
		}
		if state == "" {
			if !assert.EqualValues(t, Upgrading, man.State) {
				return
			}
		} else if state == Upgrading {
			if !assert.EqualValues(t, Ready, man.State) {
				return
			}
			if !assert.True(t, done) {
				return
			}
			break
		} else {
			t.Fatalf("invalid state")
			return
		}
		state = man.State
	}
}

func TestInstallFromGithub(t *testing.T) {
	inst, err := NewInstaller(c, &InstallerOptions{
		Slug:      "github-cozy-mini",
		SourceURL: "git://github.com/nono/cozy-mini.git",
	})
	if !assert.NoError(t, err) {
		return
	}

	go inst.InstallOrUpdate()

	var state State
	for {
		man, done, err := inst.Poll()
		if !assert.NoError(t, err) {
			return
		}
		if state == "" {
			if !assert.EqualValues(t, Installing, man.State) {
				return
			}
		} else if state == Installing {
			if !assert.EqualValues(t, Ready, man.State) {
				return
			}
			if !assert.True(t, done) {
				return
			}
			break
		} else {
			t.Fatalf("invalid state")
			return
		}
		state = man.State
	}
}

func TestMain(m *testing.M) {
	config.UseTestFile()

	db, err := checkup.HTTPChecker{URL: config.CouchURL()}.Check()
	if err != nil || db.Status() != checkup.Healthy {
		fmt.Println("This test need couchdb to run.")
		os.Exit(1)
	}

	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, manifest())
	}))

	manifestClient = &http.Client{
		Transport: &transport{},
	}

	err = couchdb.ResetDB(c, consts.Manifests)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	err = couchdb.ResetDB(c, consts.Files)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	err = vfs.CreateTrashDir(c)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	go serveGitRep()

	time.Sleep(100 * time.Millisecond)

	for _, index := range vfs.Indexes {
		err = couchdb.DefineIndex(c, consts.Files, index)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	}

	err = couchdb.ResetDB(c, consts.Permissions)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	err = couchdb.DefineIndex(c, consts.Permissions, permissions.Index)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	if err = vfs.CreateRootDirDoc(c); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	res := m.Run()

	couchdb.DeleteDB(c, consts.Manifests)
	couchdb.DeleteDB(c, consts.Files)
	ts.Close()

	localGitCmd.Process.Signal(os.Interrupt)

	os.Exit(res)
}
