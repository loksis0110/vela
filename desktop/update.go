package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/velaapp/vela/core"
)

// updateURL describes the newest Windows build. CI uploads it next to
// Vela.exe in the "latest" release.
const updateURL = "https://github.com/loksis0110/vela/releases/download/latest/version.json"

// UpdateInfo is version.json.
type UpdateInfo struct {
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
	Notes   string `json:"notes,omitempty"`
	NotesEn string `json:"notesEn,omitempty"`
	// URL of the exe; by default Vela.exe next to version.json.
	URL string `json:"url,omitempty"`
}

// UpdateState is shown in the window.
type UpdateState struct {
	Available  string  `json:"available,omitempty"` // newer version
	Notes      string  `json:"notes,omitempty"`
	Size       int64   `json:"size,omitempty"`
	Checking   bool    `json:"checking,omitempty"`
	Installing bool    `json:"installing,omitempty"`
	Progress   float64 `json:"progress,omitempty"` // 0..1 while downloading
	Error      string  `json:"error,omitempty"`
	CheckedAt  int64   `json:"checkedAt,omitempty"`
}

// compareVersions compares "1.4.0", "v1.4.1", "1.5.0-beta.2" semantically.
func compareVersions(a, b string) int {
	parse := func(v string) ([]int, string) {
		v = strings.TrimPrefix(strings.TrimSpace(v), "v")
		main, pre, _ := strings.Cut(v, "-")
		main, _, _ = strings.Cut(main, "+")
		var nums []int
		for _, p := range strings.Split(main, ".") {
			n, _ := strconv.Atoi(p)
			nums = append(nums, n)
		}
		for len(nums) < 3 {
			nums = append(nums, 0)
		}
		return nums, pre
	}
	na, pa := parse(a)
	nb, pb := parse(b)
	for i := 0; i < max(len(na), len(nb)); i++ {
		x, y := 0, 0
		if i < len(na) {
			x = na[i]
		}
		if i < len(nb) {
			y = nb[i]
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	switch {
	case pa == pb:
		return 0
	case pa == "": // a release is newer than its pre-releases
		return 1
	case pb == "":
		return -1
	}
	sa, sb := strings.Split(pa, "."), strings.Split(pb, ".")
	for i := 0; i < min(len(sa), len(sb)); i++ {
		x, errx := strconv.Atoi(sa[i])
		y, erry := strconv.Atoi(sb[i])
		switch {
		case errx == nil && erry == nil && x != y:
			if x < y {
				return -1
			}
			return 1
		case (errx != nil || erry != nil) && sa[i] != sb[i]:
			if sa[i] < sb[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(sa) < len(sb):
		return -1
	case len(sa) > len(sb):
		return 1
	}
	return 0
}

func (a *App) kickUpdateCheck() {
	select {
	case a.updateKick <- struct{}{}:
	default:
	}
}

// updateLoop checks for a new version at start and every 6 hours.
func (a *App) updateLoop() {
	timer := time.NewTimer(20 * time.Second)
	for {
		select {
		case <-timer.C:
		case <-a.updateKick:
			timer.Stop()
		}
		a.mu.Lock()
		off := a.state.Prefs.NoUpdateCheck
		a.mu.Unlock()
		if !off {
			a.CheckUpdate(false) // quietly: failures are not shown
		}
		timer.Reset(6 * time.Hour)
	}
}

// CheckUpdate downloads version.json. It returns the newer version or "".
func (a *App) CheckUpdate(manual bool) (string, error) {
	a.mu.Lock()
	if a.update.Checking || a.update.Installing {
		a.mu.Unlock()
		return a.update.Available, nil
	}
	a.update.Checking = true
	a.mu.Unlock()
	a.pushState()

	info, err := a.fetchUpdateInfo()
	a.mu.Lock()
	a.update.Checking = false
	if err != nil {
		if manual {
			a.update.Error = tr("Не удалось проверить обновления")
		}
		a.mu.Unlock()
		a.pushState()
		if manual {
			return "", errors.New(tr("Не удалось проверить обновления"))
		}
		return "", nil
	}
	a.update.Error = ""
	a.update.CheckedAt = time.Now().Unix()
	if compareVersions(info.Version, Version) > 0 {
		a.updateInfo = info
		a.update.Available = info.Version
		a.update.Size = info.Size
		a.update.Notes = info.Notes
		if language() == "en" && info.NotesEn != "" {
			a.update.Notes = info.NotesEn
		}
	} else {
		a.updateInfo = nil
		a.update.Available, a.update.Notes, a.update.Size = "", "", 0
	}
	available := a.update.Available
	a.mu.Unlock()
	a.pushState()
	return available, nil
}

func (a *App) fetchUpdateInfo() (*UpdateInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, updateURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Vela/"+Version+" (Windows)")
	resp, err := core.HTTPClient(a.routeDialer(), 20*time.Second).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("version.json: %s", resp.Status)
	}
	var info UpdateInfo
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&info); err != nil {
		return nil, err
	}
	info.SHA256 = strings.ToLower(strings.TrimSpace(info.SHA256))
	if sum, err := hex.DecodeString(info.SHA256); err != nil || len(sum) != sha256.Size {
		return nil, errors.New("version.json: bad sha256")
	}
	if info.Version == "" || info.Size < 1<<20 || info.Size > 400<<20 {
		return nil, errors.New("version.json: bad version or size")
	}
	return &info, nil
}

// InstallUpdate downloads the new Vela.exe next to the running one, checks
// its size and SHA-256, swaps the files and restarts Vela. The connection
// and admin rights are kept.
func (a *App) InstallUpdate() error {
	a.mu.Lock()
	info := a.updateInfo
	if info == nil {
		a.mu.Unlock()
		return errors.New(tr("Новая версия не найдена. Проверьте обновления ещё раз"))
	}
	if a.update.Installing {
		a.mu.Unlock()
		return nil
	}
	a.update.Installing, a.update.Progress, a.update.Error = true, 0, ""
	a.mu.Unlock()
	a.pushState()
	go func() {
		err := a.installUpdate(info)
		if err == nil {
			return // Vela is restarting
		}
		a.mu.Lock()
		a.update.Installing, a.update.Progress = false, 0
		a.update.Error = err.Error()
		a.mu.Unlock()
		a.pushState()
	}()
	return nil
}

func exePaths() (exe, old, tmp string, err error) {
	exe, err = os.Executable()
	if err != nil {
		return
	}
	if p, e := filepath.EvalSymlinks(exe); e == nil {
		exe = p
	}
	dir, base := filepath.Dir(exe), strings.TrimSuffix(filepath.Base(exe), filepath.Ext(exe))
	return exe, filepath.Join(dir, base+".old.exe"), filepath.Join(dir, base+".update.tmp"), nil
}

func (a *App) installUpdate(info *UpdateInfo) error {
	exe, old, tmp, err := exePaths()
	if err != nil {
		return err
	}
	exeURL := info.URL
	if exeURL == "" {
		exeURL = strings.TrimSuffix(updateURL, "version.json") + "Vela.exe"
	}
	if err := a.downloadUpdate(exeURL, tmp, info); err != nil {
		os.Remove(tmp)
		return err
	}
	// a running exe can be renamed but not overwritten
	os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		os.Remove(tmp)
		return fmt.Errorf(tr("не удалось заменить файл Vela: %v"), err)
	}
	if err := os.Rename(tmp, exe); err != nil {
		os.Rename(old, exe)
		os.Remove(tmp)
		return fmt.Errorf(tr("не удалось заменить файл Vela: %v"), err)
	}
	a.mu.Lock()
	connect := a.intent || a.status.State == "connected"
	a.mu.Unlock()
	args := []string{"-restarted", "-from=" + Version}
	if !embeddedWindowOpen() {
		args = append(args, "-hidden") // a browser window reconnects by itself
	}
	if connect {
		args = append(args, "-connect")
	}
	// the open window reconnects to the new Vela with the same token
	env := append(os.Environ(), "VELA_UI_TOKEN="+theServer.token)
	if err := startProcess(exe, args, env); err != nil {
		os.Rename(exe, tmp)
		os.Rename(old, exe)
		os.Remove(tmp)
		return fmt.Errorf(tr("не удалось запустить новую версию: %v"), err)
	}
	a.broadcast("restart", map[string]string{"version": info.Version})
	go func() {
		time.Sleep(300 * time.Millisecond)
		quit(a)
	}()
	return nil
}

func (a *App) downloadUpdate(url, path string, info *UpdateInfo) error {
	failed := errors.New(tr("Не удалось скачать обновление. Попробуйте позже"))
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return failed
	}
	req.Header.Set("User-Agent", "Vela/"+Version+" (Windows)")
	resp, err := core.HTTPClient(a.routeDialer(), 15*time.Minute).Do(req)
	if err != nil {
		return failed
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return failed
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return fmt.Errorf(tr("нет доступа к папке Vela: %v"), err)
	}
	hash := sha256.New()
	var done int64
	buf := make([]byte, 256<<10)
	lastPush := time.Time{}
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if done+int64(n) > info.Size {
				f.Close()
				return errors.New(tr("Обновление повреждено: размер не совпал"))
			}
			if _, err := f.Write(buf[:n]); err != nil {
				f.Close()
				return fmt.Errorf(tr("нет доступа к папке Vela: %v"), err)
			}
			hash.Write(buf[:n])
			done += int64(n)
			if time.Since(lastPush) > 250*time.Millisecond {
				lastPush = time.Now()
				a.mu.Lock()
				a.update.Progress = float64(done) / float64(info.Size)
				a.mu.Unlock()
				a.broadcast("update", map[string]float64{"progress": float64(done) / float64(info.Size)})
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			return failed
		}
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf(tr("нет доступа к папке Vela: %v"), err)
	}
	if done != info.Size {
		return errors.New(tr("Обновление повреждено: размер не совпал"))
	}
	if hex.EncodeToString(hash.Sum(nil)) != info.SHA256 {
		return errors.New(tr("Обновление повреждено: контрольная сумма не совпала"))
	}
	return nil
}

// cleanupAfterUpdate deletes the previous exe once it has exited.
func cleanupAfterUpdate() {
	_, old, tmp, err := exePaths()
	if err != nil {
		return
	}
	os.Remove(tmp)
	for i := 0; i < 40; i++ {
		if err := os.Remove(old); err == nil || os.IsNotExist(err) {
			return
		}
		time.Sleep(1500 * time.Millisecond)
	}
}
