// Package sample is a sample server driver
package sample

import (
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"time"

	"github.com/fclairamb/ftpserver/server"
	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/naoina/toml"
)

// MainDriver defines a very basic serverftp driver
type MainDriver struct {
	Logger       log.Logger  // Logger (probably shared with other components)
	SettingsFile string      // Settings file
	BaseDir      string      // Base directory from which to serve file
	tlsConfig    *tls.Config // TLS config (if applies)
}

// WelcomeUser is called to send the very first welcome message
func (driver *MainDriver) WelcomeUser(cc server.ClientContext) (string, error) {
	cc.SetDebug(true)
	// This will remain the official name for now
	return fmt.Sprintf("Welcome on ftpserver, you're on dir %s", driver.BaseDir), nil
}

// AuthUser authenticates the user and selects an handling driver
func (driver *MainDriver) AuthUser(cc server.ClientContext, user, pass string) (server.ClientHandlingDriver, error) {
	if user == "bad" || pass == "bad" {
		return nil, errors.New("bad username or password")
	}

	return driver, nil
}

// GetTLSConfig returns a TLS Certificate to use
func (driver *MainDriver) GetTLSConfig() (*tls.Config, error) {
	if driver.tlsConfig == nil {
		level.Info(driver.Logger).Log("msg", "Loading certificate")
		if cert, err := tls.LoadX509KeyPair("sample/certs/mycert.crt", "sample/certs/mycert.key"); err == nil {
			driver.tlsConfig = &tls.Config{
				NextProtos:   []string{"ftp"},
				Certificates: []tls.Certificate{cert},
			}
		} else {
			return nil, err
		}
	}
	return driver.tlsConfig, nil
}

// ChangeDirectory changes the current working directory
func (driver *MainDriver) ChangeDirectory(cc server.ClientContext, directory string) error {
	if directory == "/debug" {
		cc.SetDebug(!cc.Debug())
		return nil
	} else if directory == "/virtual" {
		return nil
	}
	_, err := os.Stat(driver.BaseDir + directory)
	return err
}

// MakeDirectory creates a directory
func (driver *MainDriver) MakeDirectory(cc server.ClientContext, directory string) error {
	return os.Mkdir(driver.BaseDir+directory, 0777)
}

// ListFiles lists the files of a directory
func (driver *MainDriver) ListFiles(cc server.ClientContext) ([]os.FileInfo, error) {

	if cc.Path() == "/virtual" {
		files := make([]os.FileInfo, 0)
		files = append(files,
			virtualFileInfo{
				name: "localpath.txt",
				mode: os.FileMode(0666),
				size: 1024,
			},
			virtualFileInfo{
				name: "file2.txt",
				mode: os.FileMode(0666),
				size: 2048,
			},
		)
		return files, nil
	}

	path := driver.BaseDir + cc.Path()

	files, err := ioutil.ReadDir(path)

	// We add a virtual dir
	if cc.Path() == "/" && err == nil {
		files = append(files, virtualFileInfo{
			name: "virtual",
			mode: os.FileMode(0666) | os.ModeDir,
			size: 4096,
		})
	}

	return files, err
}

// UserLeft is called when the user disconnects, even if he never authenticated
func (driver *MainDriver) UserLeft(cc server.ClientContext) {

}

// OpenFile opens a file in 3 possible modes: read, write, appending write (use appropriate flags)
func (driver *MainDriver) OpenFile(cc server.ClientContext, path string, flag int) (server.FileStream, error) {

	if path == "/virtual/localpath.txt" {
		return &virtualFile{content: []byte(driver.BaseDir)}, nil
	}

	path = driver.BaseDir + path

	// If we are writing and we are not in append mode, we should remove the file
	if (flag & os.O_WRONLY) != 0 {
		flag |= os.O_CREATE
		if (flag & os.O_APPEND) == 0 {
			os.Remove(path)
		}
	}

	return os.OpenFile(path, flag, 0666)
}

// GetFileInfo gets some info around a file or a directory
func (driver *MainDriver) GetFileInfo(cc server.ClientContext, path string) (os.FileInfo, error) {
	path = driver.BaseDir + path

	return os.Stat(path)
}

// CanAllocate gives the approval to allocate some data
func (driver *MainDriver) CanAllocate(cc server.ClientContext, size int) (bool, error) {
	return true, nil
}

// ChmodFile changes the attributes of the file
func (driver *MainDriver) ChmodFile(cc server.ClientContext, path string, mode os.FileMode) error {
	path = driver.BaseDir + path

	return os.Chmod(path, mode)
}

// DeleteFile deletes a file or a directory
func (driver *MainDriver) DeleteFile(cc server.ClientContext, path string) error {
	path = driver.BaseDir + path

	return os.Remove(path)
}

// RenameFile renames a file or a directory
func (driver *MainDriver) RenameFile(cc server.ClientContext, from, to string) error {
	from = driver.BaseDir + from
	to = driver.BaseDir + to

	return os.Rename(from, to)
}

// GetSettings returns some general settings around the server setup
func (driver *MainDriver) GetSettings() *server.Settings {
	f, err := os.Open(driver.SettingsFile)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	buf, err := ioutil.ReadAll(f)
	if err != nil {
		panic(err)
	}
	var config server.Settings
	if err := toml.Unmarshal(buf, &config); err != nil {
		panic(err)
	}

	// This is the new IP loading change coming from Ray
	if config.PublicHost == "" {
		level.Debug(driver.Logger).Log("msg", "Fetching our external IP address...")
		if config.PublicHost, err = externalIP(); err != nil {
			level.Warn(driver.Logger).Log("msg", "Couldn't fetch an external IP", "err", err)
		} else {
			level.Debug(driver.Logger).Log("msg", "Fetched our external IP address", "ipAddress", config.PublicHost)
		}
	}

	return &config
}

// NewSampleDriver creates a sample driver
// Note: This is not a mistake. Interface can be pointers. There seems to be a lot of confusion around this in the
//       server_ftp original code.
func NewSampleDriver() (*MainDriver, error) {
	dir, err := ioutil.TempDir("", "ftpserver")
	if err != nil {
		return nil, fmt.Errorf("could not find a temporary dir, err: %v", err)
	}

	driver := &MainDriver{
		Logger:       log.NewNopLogger(),
		SettingsFile: "sample/conf/settings.toml",
		BaseDir:      dir,
	}

	// This is also a good time to create our dir
	os.MkdirAll(driver.BaseDir, 0777)

	return driver, nil
}

type virtualFile struct {
	content    []byte // Content of the file
	readOffset int    // Reading offset
}

func (f *virtualFile) Close() error {
	return nil
}

func (f *virtualFile) Read(buffer []byte) (int, error) {
	n := copy(buffer, f.content[f.readOffset:])
	f.readOffset += n
	if n == 0 {
		return 0, io.EOF
	}

	return n, nil
}

func (f *virtualFile) Seek(n int64, w int) (int64, error) {
	return 0, nil
}

func (f *virtualFile) Write(buffer []byte) (int, error) {
	return 0, nil
}

type virtualFileInfo struct {
	name string
	size int64
	mode os.FileMode
}

func (f virtualFileInfo) Name() string {
	return f.name
}

func (f virtualFileInfo) Size() int64 {
	return f.size
}

func (f virtualFileInfo) Mode() os.FileMode {
	return f.mode
}

func (f virtualFileInfo) IsDir() bool {
	return f.mode.IsDir()
}

func (f virtualFileInfo) ModTime() time.Time {
	return time.Now().UTC()
}

func (f virtualFileInfo) Sys() interface{} {
	return nil
}

func externalIP() (string, error) {
	// If you need to take a bet, amazon is about as reliable & sustainable a service as you can get
	rsp, err := http.Get("http://checkip.amazonaws.com")
	if err != nil {
		return "", err
	}
	defer rsp.Body.Close()

	buf, err := ioutil.ReadAll(rsp.Body)
	if err != nil {
		return "", err
	}

	return string(bytes.TrimSpace(buf)), nil
}
