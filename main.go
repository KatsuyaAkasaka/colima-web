// colima-web: ローカルで colima CLI をラップする最小 Web サービス。
// colima 自体は再実装せず、既存バイナリを os/exec で実行する。
package main

import (
	"bufio"
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

//go:embed web
var webFS embed.FS

var colimaBin = "colima"
var dockerBin = "docker"

func main() {
	addr := flag.String("addr", "127.0.0.1:51900", "listen address (localhost 推奨)")
	flag.Parse()

	if v := os.Getenv("COLIMA_BIN"); v != "" {
		colimaBin = v
	}
	if v := os.Getenv("DOCKER_BIN"); v != "" {
		dockerBin = v
	}

	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(sub)))
	mux.HandleFunc("/api/instances", handleInstances)
	mux.HandleFunc("/api/containers", handleContainers)
	mux.HandleFunc("/api/container", handleContainerAction)
	mux.HandleFunc("/api/container/logs", handleContainerLogs)
	mux.HandleFunc("/api/images", handleImages)
	mux.HandleFunc("/api/image", handleImageAction)
	mux.HandleFunc("/api/prune", handlePrune)
	mux.HandleFunc("/api/version", handleVersion)
	mux.HandleFunc("/api/action", handleAction)

	log.Printf("colima-web listening on http://%s (colima=%s)", *addr, colimaBin)
	srv := &http.Server{Addr: *addr, Handler: logRequests(mux)}
	if err := srv.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

func logRequests(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.Path)
		h.ServeHTTP(w, r)
	})
}

// Instance は `colima list --json` の各行に対応する。
type Instance struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Arch    string `json:"arch"`
	CPUs    int    `json:"cpus"`
	Memory  int64  `json:"memory"`
	Disk    int64  `json:"disk"`
	Runtime string `json:"runtime"`
}

// GET /api/instances -> colima list --json を JSON 配列で返す。
func handleInstances(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, colimaBin, "list", "--json").Output()
	if err != nil {
		http.Error(w, "colima list failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var instances []Instance
	dec := json.NewDecoder(strings.NewReader(string(out)))
	for dec.More() {
		var ins Instance
		if err := dec.Decode(&ins); err != nil {
			break
		}
		instances = append(instances, ins)
	}
	writeJSON(w, instances)
}

// Container は `docker ps --format {{json .}}` の各行に対応する（必要な項目のみ）。
type Container struct {
	ID     string `json:"ID"`
	Names  string `json:"Names"`
	Image  string `json:"Image"`
	State  string `json:"State"`
	Status string `json:"Status"`
	Ports  string `json:"Ports"`
}

// colima のプロファイルに対応する docker コンテキスト名。default は "colima"。
func dockerContext(profile string) string {
	if profile == "" || profile == "default" {
		return "colima"
	}
	return "colima-" + profile
}

// GET /api/containers?profile= -> 指定プロファイルの docker コンテキストで `docker ps -a`。
func handleContainers(w http.ResponseWriter, r *http.Request) {
	profile := r.URL.Query().Get("profile")
	if !validProfile(profile) {
		http.Error(w, "invalid profile name", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, dockerBin, "--context", dockerContext(profile),
		"ps", "-a", "--format", "{{json .}}")
	out, err := cmd.CombinedOutput()
	if err != nil {
		// インスタンス未起動や containerd ランタイムでは docker が使えない。
		http.Error(w, "docker ps failed (インスタンスが起動中か、runtime=docker か確認してください): "+
			strings.TrimSpace(string(out)), http.StatusBadGateway)
		return
	}

	var containers []Container
	dec := json.NewDecoder(strings.NewReader(string(out)))
	for dec.More() {
		var c Container
		if err := dec.Decode(&c); err != nil {
			break
		}
		containers = append(containers, c)
	}
	writeJSON(w, containers)
}

// docker のコンテナID/名前として許可する文字（英数・_ . -）。
func validContainerID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}

// ContainerRequest は /api/container の本体。
type ContainerRequest struct {
	Action  string `json:"action"`  // start | stop | restart | remove
	Profile string `json:"profile"`
	ID      string `json:"id"`
}

// POST /api/container -> docker でコンテナを start/stop/restart/remove し、出力をストリーム返却。
func handleContainerAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req ContainerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if !validProfile(req.Profile) {
		http.Error(w, "invalid profile name", http.StatusBadRequest)
		return
	}
	if !validContainerID(req.ID) {
		http.Error(w, "invalid container id", http.StatusBadRequest)
		return
	}

	base := []string{"--context", dockerContext(req.Profile)}
	var args []string
	switch req.Action {
	case "start", "stop", "restart":
		args = append(base, req.Action, req.ID)
	case "remove":
		args = append(base, "rm", "-f", req.ID)
	default:
		http.Error(w, "unknown action: "+req.Action, http.StatusBadRequest)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	streamExec(w, flusher, dockerBin, "docker", args)
}

// GET /api/container/logs?profile=&id= -> docker logs の末尾を返す。
func handleContainerLogs(w http.ResponseWriter, r *http.Request) {
	profile := r.URL.Query().Get("profile")
	id := r.URL.Query().Get("id")
	if !validProfile(profile) {
		http.Error(w, "invalid profile name", http.StatusBadRequest)
		return
	}
	if !validContainerID(id) {
		http.Error(w, "invalid container id", http.StatusBadRequest)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	args := []string{"--context", dockerContext(profile), "logs", "--tail", "500", "--timestamps", id}
	streamExec(w, flusher, dockerBin, "docker", args)
}

// docker のイメージ参照 (repo:tag や registry/ns/repo@sha256:...) として許可する文字。
// 直接 exec するためシェル経由ではないが、念のため安全な文字集合に限定する。
func validImageRef(s string) bool {
	if s == "" || len(s) > 256 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			r == '-' || r == '_' || r == '.' || r == '/' || r == ':' || r == '@') {
			return false
		}
	}
	return true
}

// Image は `docker images --format {{json .}}` の各行に対応する（必要な項目のみ）。
type Image struct {
	Repository   string `json:"Repository"`
	Tag          string `json:"Tag"`
	ID           string `json:"ID"`
	Size         string `json:"Size"`
	CreatedSince string `json:"CreatedSince"`
	Containers   string `json:"Containers"`
}

// GET /api/images?profile= -> 指定プロファイルの docker コンテキストで `images`。
func handleImages(w http.ResponseWriter, r *http.Request) {
	profile := r.URL.Query().Get("profile")
	if !validProfile(profile) {
		http.Error(w, "invalid profile name", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, dockerBin, "--context", dockerContext(profile),
		"images", "--format", "{{json .}}")
	out, err := cmd.CombinedOutput()
	if err != nil {
		http.Error(w, "docker images failed (インスタンスが起動中か、runtime=docker か確認してください): "+
			strings.TrimSpace(string(out)), http.StatusBadGateway)
		return
	}

	var images []Image
	dec := json.NewDecoder(strings.NewReader(string(out)))
	for dec.More() {
		var im Image
		if err := dec.Decode(&im); err != nil {
			break
		}
		images = append(images, im)
	}
	writeJSON(w, images)
}

// ImageRequest は /api/image の本体。
type ImageRequest struct {
	Action  string `json:"action"`  // pull | remove
	Profile string `json:"profile"`
	Ref     string `json:"ref"` // pull: イメージ名 / remove: イメージID or repo:tag
}

// POST /api/image -> docker でイメージを pull / remove し、出力をストリーム返却。
func handleImageAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req ImageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if !validProfile(req.Profile) {
		http.Error(w, "invalid profile name", http.StatusBadRequest)
		return
	}
	if !validImageRef(req.Ref) {
		http.Error(w, "invalid image reference", http.StatusBadRequest)
		return
	}

	base := []string{"--context", dockerContext(req.Profile)}
	var args []string
	switch req.Action {
	case "pull":
		args = append(base, "pull", req.Ref)
	case "remove":
		args = append(base, "rmi", req.Ref)
	default:
		http.Error(w, "unknown action: "+req.Action, http.StatusBadRequest)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	streamExec(w, flusher, dockerBin, "docker", args)
}

// PruneRequest は /api/prune の本体。
type PruneRequest struct {
	Target  string `json:"target"`  // docker | colima
	Profile string `json:"profile"`
}

// POST /api/prune -> 未使用リソース（docker）/ ダウンロードキャッシュ（colima）を削除。
func handlePrune(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req PruneRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if !validProfile(req.Profile) {
		http.Error(w, "invalid profile name", http.StatusBadRequest)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	switch req.Target {
	case "docker":
		// 未使用のコンテナ/ネットワーク/イメージ/ビルドキャッシュを削除。
		args := []string{"--context", dockerContext(req.Profile), "system", "prune", "-f"}
		streamExec(w, flusher, dockerBin, "docker", args)
	case "colima":
		// colima がダウンロードした assets のキャッシュを削除。
		args := []string{"prune", "-f"}
		if req.Profile != "" {
			args = append(args, "--profile", req.Profile)
		}
		streamExec(w, flusher, colimaBin, "colima", args)
	default:
		http.Error(w, "unknown prune target: "+req.Target, http.StatusBadRequest)
	}
}

// GET /api/version -> colima version。
func handleVersion(w http.ResponseWriter, r *http.Request) {
	out, _ := exec.Command(colimaBin, "version").CombinedOutput()
	writeJSON(w, map[string]string{"version": strings.TrimSpace(string(out))})
}

// StartConfig は start フォームの入力。許可されたフィールドのみを引数に変換する
// （任意フラグの素通しはコマンドインジェクションになるため避ける）。
type StartConfig struct {
	CPUs       int    `json:"cpus"`
	Memory     int    `json:"memory"` // GiB
	Disk       int    `json:"disk"`   // GiB
	Arch       string `json:"arch"`   // aarch64 | x86_64
	Runtime    string `json:"runtime"`
	VMType     string `json:"vmType"` // qemu | vz
	Kubernetes bool   `json:"kubernetes"`
	Mounts     string `json:"mounts"` // 改行区切り
}

// ActionRequest は /api/action の本体。
type ActionRequest struct {
	Action  string      `json:"action"`  // start | stop | restart
	Profile string      `json:"profile"` // 省略時 default
	Config  StartConfig `json:"config"`  // action=start のときのみ使用
}

var allowedArch = map[string]bool{"aarch64": true, "x86_64": true}
var allowedRuntime = map[string]bool{"docker": true, "containerd": true, "incus": true}
var allowedVMType = map[string]bool{"qemu": true, "vz": true}

// 許可リストに基づいて start 用のコマンド引数を組み立てる。
func (c StartConfig) args() ([]string, error) {
	var a []string
	if c.CPUs > 0 {
		a = append(a, "--cpu", strconv.Itoa(c.CPUs))
	}
	if c.Memory > 0 {
		a = append(a, "--memory", strconv.Itoa(c.Memory))
	}
	if c.Disk > 0 {
		a = append(a, "--disk", strconv.Itoa(c.Disk))
	}
	if c.Arch != "" {
		if !allowedArch[c.Arch] {
			return nil, fmt.Errorf("invalid arch: %s", c.Arch)
		}
		a = append(a, "--arch", c.Arch)
	}
	if c.Runtime != "" {
		if !allowedRuntime[c.Runtime] {
			return nil, fmt.Errorf("invalid runtime: %s", c.Runtime)
		}
		a = append(a, "--runtime", c.Runtime)
	}
	if c.VMType != "" {
		if !allowedVMType[c.VMType] {
			return nil, fmt.Errorf("invalid vmType: %s", c.VMType)
		}
		a = append(a, "--vm-type", c.VMType)
	}
	if c.Kubernetes {
		a = append(a, "--kubernetes")
	}
	for _, line := range strings.Split(c.Mounts, "\n") {
		if m := strings.TrimSpace(line); m != "" {
			a = append(a, "--mount", m)
		}
	}
	return a, nil
}

// プロファイル名は colima の命名規則に合わせて英数・ハイフン・アンダースコアのみ許可。
func validProfile(p string) bool {
	if p == "" {
		return true
	}
	for _, r := range p {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// POST /api/action -> colima を実行し、stdout/stderr を逐次ストリーム返却する。
func handleAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req ActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if !validProfile(req.Profile) {
		http.Error(w, "invalid profile name", http.StatusBadRequest)
		return
	}

	var args []string
	switch req.Action {
	case "start":
		args = []string{"start"}
		if req.Profile != "" {
			args = append(args, req.Profile)
		}
		extra, err := req.Config.args()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		args = append(args, extra...)
	case "stop", "restart":
		args = []string{req.Action}
		if req.Profile != "" {
			args = append(args, req.Profile)
		}
	case "delete":
		// -f で対話プロンプトを回避（確認はフロント側で実施）。
		args = []string{"delete", "-f"}
		if req.Profile != "" {
			args = append(args, req.Profile)
		}
	default:
		http.Error(w, "unknown action: "+req.Action, http.StatusBadRequest)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	streamExec(w, flusher, colimaBin, "colima", args)
}

// バイナリを実行し、stdout/stderr を行単位で w へ逐次フラッシュする。
// VM 操作は数分かかるため context は引かない（クライアント切断で止めない）。
// display は先頭に表示するコマンド名（実体のパスは隠す）。
func streamExec(w http.ResponseWriter, flusher http.Flusher, bin, display string, args []string) {
	fmt.Fprintf(w, "$ %s %s\n", display, strings.Join(args, " "))
	flusher.Flush()

	cmd := exec.Command(bin, args...)
	// colima は進捗ログを stderr に出すため stdout/stderr を1本のパイプに統合する。
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(w, "error: %v\n", err)
		return
	}
	// Wait は一度しか呼べないので goroutine 側で呼び、結果をチャネルで受け取る。
	done := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		pw.Close() // scanner に EOF を伝える
		done <- err
	}()

	scanner := bufio.NewScanner(pr)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		fmt.Fprintf(w, "%s\n", scanner.Text())
		flusher.Flush()
	}

	if err := <-done; err != nil {
		fmt.Fprintf(w, "\n[exit] %v\n", err)
	} else {
		fmt.Fprintf(w, "\n[done]\n")
	}
	flusher.Flush()
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
