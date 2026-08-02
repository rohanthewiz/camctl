package handlers

import (
	"camctl/ndi"
	"camctl/storage"
	"camctl/views"
	"camctl/visca"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rohanthewiz/rweb"
	"github.com/rohanthewiz/serr"
)

// defaultPanSpeed and defaultTiltSpeed provide moderate movement rates.
// VISCA allows pan 0x01–0x18 and tilt 0x01–0x17.
const (
	defaultPanSpeed  byte = 0x08
	defaultTiltSpeed byte = 0x08
)

// App holds shared state across all HTTP handlers: the VISCA client,
// storage, and current camera settings.
// The mutex protects the VISCA client during reconnection.
type App struct {
	mu       sync.RWMutex
	Camera   *visca.Client
	Store    *storage.DB
	NDI      ndi.Previewer
	Settings views.Settings
}

// NewApp creates an App with the given storage.
// The VISCA client starts disconnected — the user connects via the settings UI.
func NewApp(store *storage.DB) *App {
	return &App{
		Store: store,
		NDI:   ndi.NewPreviewer(),
		Settings: views.Settings{
			CameraPort: visca.DefaultPort,
		},
	}
}

// formValue reads a form field and returns a copy of it.
//
// rweb hands back strings that alias the connection's request buffer — an
// unsafe zero-copy []byte-to-string view (see rweb's b2s/GetPostValue). That
// buffer is reused for the next request on the connection, so any value the app
// keeps past the current handler silently mutates into whatever arrived later:
// a saved camera IP of "127.0.0.1" would turn into the first nine bytes of some
// unrelated form body.
//
// Everything stored in App.Settings or used as a preset lookup key outlives its
// request, so all reads go through here rather than leaving the distinction to
// be re-derived at each call site.
func formValue(c rweb.Context, key string) string {
	return strings.Clone(c.Request().FormValue(key))
}

// RegisterRoutes wires all handlers to the rweb server.
func (a *App) RegisterRoutes(s *rweb.Server) {
	// Readiness probe — used by the macOS app wrapper to know when the
	// server is up before loading the UI; cheaper than rendering the index.
	s.Get("/health", func(c rweb.Context) error {
		return c.WriteJSON(map[string]string{"status": "ok"})
	})
	s.Get("/", a.handleIndex)
	s.Post("/api/move", a.handleMove)
	s.Post("/api/zoom", a.handleZoom)
	s.Post("/api/preset/recall", a.handlePresetRecall)
	s.Post("/api/preset/set", a.handlePresetSet)
	s.Post("/api/preset/label", a.handlePresetLabel)
	s.Post("/api/settings", a.handleSettings)
	s.Post("/api/camera/remove", a.handleCameraRemove)
	s.Post("/api/camera/edit", a.handleCameraEdit)
	s.Post("/api/preview/settings", a.handlePreviewSettings)
	s.Get("/api/preview", a.handlePreview)
}

// handleIndex renders the full page with current settings, presets, and saved cameras.
func (a *App) handleIndex(c rweb.Context) error {
	a.mu.RLock()
	settings := a.Settings
	a.mu.RUnlock()

	rawCams, err := a.Store.AllCameras()
	if err != nil {
		log.Printf("AllCameras: %v", err)
	}
	camItems := make([]views.CameraItem, len(rawCams))
	for i, cam := range rawCams {
		camItems[i] = views.CameraItem{Label: cam.Label, IP: cam.IP, Port: cam.Port}
	}

	// Presets are per-camera, so the grid shows the active camera's slots.
	// With nothing selected this yields placeholder labels (see AllPresets).
	presets := a.presetsFor(settings.CameraLabel)

	previewSettings, err := a.Store.GetPreviewSettings()
	if err != nil {
		log.Printf("GetPreviewSettings: %v", err)
	}

	data := views.PageData{
		Settings:        settings,
		Presets:         presets,
		Cameras:         camItems,
		PreviewSettings: previewSettings,
	}
	return c.WriteHTML(views.RenderPage(data))
}

// handleMove processes pan/tilt/home/stop commands.
// Expects form param "direction": left, right, up, down, home, stop.
// Optional "panSpeed" (1–24) and "tiltSpeed" (1–23) override the defaults,
// enabling client-side speed curves to ramp movement over time.
func (a *App) handleMove(c rweb.Context) error {
	direction := formValue(c, "direction")

	// Parse optional speed overrides — the JS ramping logic sends these
	// with increasing values while a D-pad button is held down.
	panSpeed := defaultPanSpeed
	tiltSpeed := defaultTiltSpeed
	if ps := formValue(c, "panSpeed"); ps != "" {
		if v, err := strconv.Atoi(ps); err == nil && v >= 1 && v <= 0x18 {
			panSpeed = byte(v)
		}
	}
	if ts := formValue(c, "tiltSpeed"); ts != "" {
		if v, err := strconv.Atoi(ts); err == nil && v >= 1 && v <= 0x17 {
			tiltSpeed = byte(v)
		}
	}

	a.mu.RLock()
	cam := a.Camera
	a.mu.RUnlock()

	if cam == nil || !cam.IsConnected() {
		return c.WriteJSON(map[string]string{"error": "not connected"})
	}

	var err error
	switch direction {
	case "left":
		err = cam.PanTilt(visca.DirLeft, visca.DirStop, panSpeed, tiltSpeed)
	case "right":
		err = cam.PanTilt(visca.DirRight, visca.DirStop, panSpeed, tiltSpeed)
	case "up":
		err = cam.PanTilt(visca.DirStop, visca.DirUp, panSpeed, tiltSpeed)
	case "down":
		err = cam.PanTilt(visca.DirStop, visca.DirDown, panSpeed, tiltSpeed)
	case "home":
		err = cam.Home()
	case "stop":
		err = cam.Stop()
	default:
		return c.WriteJSON(map[string]string{"error": "unknown direction"})
	}

	if err != nil {
		return serr.Wrap(err, "move command failed", "direction", direction)
	}
	return c.WriteJSON(map[string]string{"status": "ok"})
}

// handleZoom processes zoom in/out/stop commands.
// Expects form params "action" (in/out/stop) and optional "speed" (1–7).
func (a *App) handleZoom(c rweb.Context) error {
	action := formValue(c, "action")
	speedStr := formValue(c, "speed")

	// Parse speed, default to 4 (moderate)
	speed := byte(4)
	if speedStr != "" {
		if s, err := strconv.Atoi(speedStr); err == nil && s >= 1 && s <= 7 {
			speed = byte(s)
		}
	}

	a.mu.RLock()
	cam := a.Camera
	a.mu.RUnlock()

	if cam == nil || !cam.IsConnected() {
		return c.WriteJSON(map[string]string{"error": "not connected"})
	}

	var err error
	switch action {
	case "in":
		err = cam.ZoomIn(speed)
	case "out":
		err = cam.ZoomOut(speed)
	case "stop":
		err = cam.ZoomStop()
	default:
		return c.WriteJSON(map[string]string{"error": "unknown zoom action"})
	}

	if err != nil {
		return serr.Wrap(err, "zoom command failed", "action", action)
	}
	return c.WriteJSON(map[string]string{"status": "ok"})
}

// handlePresetRecall recalls a saved camera position.
// Expects form param "num" (0–5 for our 6 presets).
func (a *App) handlePresetRecall(c rweb.Context) error {
	num, err := strconv.Atoi(formValue(c, "num"))
	if err != nil || num < 0 || num > 5 {
		return c.WriteJSON(map[string]string{"error": "invalid preset number"})
	}

	a.mu.RLock()
	cam := a.Camera
	a.mu.RUnlock()

	if cam == nil || !cam.IsConnected() {
		return c.WriteJSON(map[string]string{"error": "not connected"})
	}

	if err := cam.PresetRecall(byte(num)); err != nil {
		return serr.Wrap(err, "preset recall failed", "num", fmt.Sprintf("%d", num))
	}
	return c.WriteJSON(map[string]string{"status": "ok"})
}

// handlePresetSet saves the current camera position to a preset slot.
// Expects form param "num" (0–5).
func (a *App) handlePresetSet(c rweb.Context) error {
	num, err := strconv.Atoi(formValue(c, "num"))
	if err != nil || num < 0 || num > 5 {
		return c.WriteJSON(map[string]string{"error": "invalid preset number"})
	}

	a.mu.RLock()
	cam := a.Camera
	a.mu.RUnlock()

	if cam == nil || !cam.IsConnected() {
		return c.WriteJSON(map[string]string{"error": "not connected"})
	}

	if err := cam.PresetSet(byte(num)); err != nil {
		return serr.Wrap(err, "preset set failed", "num", fmt.Sprintf("%d", num))
	}
	return c.WriteJSON(map[string]string{"status": "ok"})
}

// handlePresetLabel updates a preset's display label and persists it against
// the active camera. Expects form params "num" (0–5) and "label" (text).
//
// The camera is taken from server-side state rather than a client-supplied
// field: the label the operator is editing is by definition the one on screen,
// which is the camera the server considers active.
func (a *App) handlePresetLabel(c rweb.Context) error {
	num, err := strconv.Atoi(formValue(c, "num"))
	if err != nil || num < 0 || num > 5 {
		return c.WriteJSON(map[string]string{"error": "invalid preset number"})
	}

	a.mu.RLock()
	cameraLabel := a.Settings.CameraLabel
	a.mu.RUnlock()

	if cameraLabel == "" {
		return c.WriteJSON(map[string]string{"error": "select a camera before naming presets"})
	}

	label := formValue(c, "label")
	if err := a.Store.UpdatePresetLabel(cameraLabel, num, label); err != nil {
		return serr.Wrap(err, "preset label update failed")
	}
	return c.WriteJSON(map[string]string{"status": "ok"})
}

// presetsFor loads a camera's preset labels, falling back to placeholders so a
// storage error degrades to an unnamed-but-usable grid instead of an empty one.
func (a *App) presetsFor(cameraLabel string) []storage.Preset {
	presets, err := a.Store.AllPresets(cameraLabel)
	if err != nil {
		log.Printf("AllPresets(%q): %v", cameraLabel, err)
		return storage.DefaultPresets()
	}
	return presets
}

// cameraJSON represents a saved camera in the settings response.
type cameraJSON struct {
	Label string `json:"label"`
	IP    string `json:"ip"`
	Port  int    `json:"port"`
}

// presetJSON represents one preset slot in the settings response.
type presetJSON struct {
	Number int    `json:"number"`
	Label  string `json:"label"`
}

// settingsResponse is the JSON shape returned after a connection attempt.
// Includes the full saved-cameras list so the client can update the sidebar
// without a page reload, plus the active camera's presets — those are
// per-camera, so switching cameras has to repaint the preset grid.
type settingsResponse struct {
	Connected bool         `json:"connected"`
	Label     string       `json:"label"`
	IP        string       `json:"ip"`
	Port      int          `json:"port"`
	Error     string       `json:"error,omitempty"`
	Cameras   []cameraJSON `json:"cameras"`
	Presets   []presetJSON `json:"presets"`
}

// presetsJSON loads a camera's presets in the JSON response format.
func (a *App) presetsJSON(cameraLabel string) []presetJSON {
	raw := a.presetsFor(cameraLabel)
	out := make([]presetJSON, len(raw))
	for i, p := range raw {
		out[i] = presetJSON{Number: p.Number, Label: p.Label}
	}
	return out
}

// handleSettings connects to a camera and saves it to the camera list when a
// label is provided. Used for both one-click saved-camera connections and
// new camera additions from the add-camera form.
// Expects form params "label" (required for saving), "ip", and "port".
func (a *App) handleSettings(c rweb.Context) error {
	label := strings.TrimSpace(formValue(c, "label"))
	ip := strings.TrimSpace(formValue(c, "ip"))
	portStr := formValue(c, "port")

	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		port = visca.DefaultPort
	}

	a.mu.Lock()
	// Tear down any existing connection before reconnecting.
	if a.Camera != nil {
		_ = a.Camera.Close()
	}
	a.NDI.Stop()

	cam := visca.NewClient(ip, port)
	connectErr := cam.Connect()

	if connectErr != nil {
		a.Camera = nil
		a.Settings = views.Settings{CameraLabel: label, CameraIP: ip, CameraPort: port, Connected: false}
		a.mu.Unlock()

		return c.WriteJSON(settingsResponse{
			Connected: false,
			Label:     label,
			IP:        ip,
			Port:      port,
			Error:     connectErr.Error(),
			Cameras:   a.savedCamerasJSON(),
			Presets:   a.presetsJSON(label),
		})
	}

	a.Camera = cam
	a.Settings = views.Settings{CameraLabel: label, CameraIP: ip, CameraPort: port, Connected: true}
	a.mu.Unlock()

	// Start preview in background using stored protocol preferences.
	go func() {
		opts := a.buildPreviewOptions()
		if err := a.NDI.Start(ip, opts); err != nil {
			log.Printf("preview: %v", err)
		}
	}()

	// Persist the camera to the saved list when a label is provided.
	// UpsertCamera also provisions this camera's own preset slots, so the
	// presets read below reflect the new camera rather than the previous one.
	if label != "" {
		_ = a.Store.UpsertCamera(storage.Camera{Label: label, IP: ip, Port: port})
	}

	return c.WriteJSON(settingsResponse{
		Connected: true,
		Label:     label,
		IP:        ip,
		Port:      port,
		Cameras:   a.savedCamerasJSON(),
		Presets:   a.presetsJSON(label),
	})
}

// savedCamerasJSON converts the camera store into the JSON response format.
func (a *App) savedCamerasJSON() []cameraJSON {
	raw, err := a.Store.AllCameras()
	if err != nil {
		log.Printf("savedCamerasJSON: %v", err)
		return nil
	}
	out := make([]cameraJSON, len(raw))
	for i, cam := range raw {
		out[i] = cameraJSON{Label: cam.Label, IP: cam.IP, Port: cam.Port}
	}
	return out
}

// handleCameraRemove removes a camera from the saved list.
// Expects form param "label".
func (a *App) handleCameraRemove(c rweb.Context) error {
	label := formValue(c, "label")
	if err := a.Store.RemoveCamera(label); err != nil {
		return serr.Wrap(err, "camera remove failed")
	}
	// Snapshot settings under the read lock — handleSettings mutates them
	// concurrently from other requests.
	a.mu.RLock()
	settings := a.Settings
	a.mu.RUnlock()
	return c.WriteJSON(settingsResponse{
		Connected: settings.Connected,
		Label:     settings.CameraLabel,
		IP:        settings.CameraIP,
		Port:      settings.CameraPort,
		Cameras:   a.savedCamerasJSON(),
		Presets:   a.presetsJSON(settings.CameraLabel),
	})
}

// handleCameraEdit updates a saved camera's label, IP, or port.
// Expects form params "old_label", "label", "ip", "port".
func (a *App) handleCameraEdit(c rweb.Context) error {
	oldLabel := formValue(c, "old_label")
	label := formValue(c, "label")
	ip := formValue(c, "ip")
	portStr := formValue(c, "port")

	if oldLabel == "" || label == "" || ip == "" {
		return c.WriteJSON(map[string]string{"error": "label, ip are required"})
	}

	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		port = visca.DefaultPort
	}

	if err := a.Store.UpdateCamera(oldLabel, storage.Camera{Label: label, IP: ip, Port: port}); err != nil {
		return c.WriteJSON(map[string]string{"error": err.Error()})
	}

	// If the active camera was edited, update settings to match.
	// Snapshot while still holding the lock so the response reflects
	// a consistent view even if another request mutates settings.
	a.mu.Lock()
	if a.Settings.CameraLabel == oldLabel {
		a.Settings.CameraLabel = label
		a.Settings.CameraIP = ip
		a.Settings.CameraPort = port
	}
	settings := a.Settings
	a.mu.Unlock()

	return c.WriteJSON(settingsResponse{
		Connected: settings.Connected,
		Label:     settings.CameraLabel,
		IP:        settings.CameraIP,
		Port:      settings.CameraPort,
		Cameras:   a.savedCamerasJSON(),
		// A rename re-keys the camera's presets, so re-read them under the new label.
		Presets: a.presetsJSON(settings.CameraLabel),
	})
}

// buildPreviewOptions loads stored preview settings and converts them
// to the ndi.PreviewOptions expected by the previewer.
func (a *App) buildPreviewOptions() ndi.PreviewOptions {
	ps, err := a.Store.GetPreviewSettings()
	if err != nil {
		log.Printf("buildPreviewOptions: %v, using defaults", err)
		return ndi.PreviewOptions{EnableNDI: true}
	}
	return ndi.PreviewOptions{
		EnableNDI:   ps.EnableNDI,
		EnableOBS:   ps.EnableOBS,
		EnableHTTP:  ps.EnableHTTP,
		EnableRTSP:  ps.EnableRTSP,
		OBSHost:     ps.OBSWSHost,
		OBSPassword: ps.OBSWSPassword,
	}
}

// handlePreviewSettings saves preview protocol preferences and restarts the
// preview if a camera is currently connected. This lets the user toggle
// which strategies are tried without reconnecting the camera.
func (a *App) handlePreviewSettings(c rweb.Context) error {
	ps := storage.PreviewSettings{
		EnableNDI:     formValue(c, "enable_ndi") == "true",
		EnableOBS:     formValue(c, "enable_obs") == "true",
		EnableHTTP:    formValue(c, "enable_http") == "true",
		EnableRTSP:    formValue(c, "enable_rtsp") == "true",
		OBSWSHost:     strings.TrimSpace(formValue(c, "obs_ws_host")),
		OBSWSPassword: formValue(c, "obs_ws_password"),
	}

	// The settings form never echoes the stored password back (see
	// obsPasswordPlaceholder in views), so a blank field is the normal
	// "unchanged" case — preserve the saved value rather than wiping it.
	// Trade-off: a password can't be cleared from the UI, only replaced;
	// acceptable since a stale password is simply ignored when OBS auth
	// is disabled.
	if ps.OBSWSPassword == "" {
		if existing, err := a.Store.GetPreviewSettings(); err == nil {
			ps.OBSWSPassword = existing.OBSWSPassword
		}
	}

	if err := a.Store.UpdatePreviewSettings(ps); err != nil {
		return c.WriteJSON(map[string]string{"error": err.Error()})
	}

	// Restart preview with new settings if a camera is connected.
	a.mu.RLock()
	connected := a.Camera != nil && a.Camera.IsConnected()
	cameraIP := a.Settings.CameraIP
	a.mu.RUnlock()

	if connected && cameraIP != "" {
		a.NDI.Stop()
		go func() {
			opts := a.buildPreviewOptions()
			if err := a.NDI.Start(cameraIP, opts); err != nil {
				log.Printf("preview restart: %v", err)
			}
		}()
	}

	return c.WriteJSON(map[string]string{"status": "ok"})
}

// handlePreview streams NDI video frames to the client via WebSocket.
// Each message is a binary JPEG frame sent at roughly 10fps.
func (a *App) handlePreview(c rweb.Context) error {
	if !c.IsWebSocketUpgrade() {
		return c.WriteJSON(map[string]string{"error": "websocket upgrade required"})
	}

	ws, err := c.UpgradeWebSocket()
	if err != nil {
		return err
	}
	defer ws.Close(1000, "done")

	for {
		select {
		case <-ws.Done():
			return nil
		default:
		}

		frame := a.NDI.Frame()
		if len(frame) == 0 {
			time.Sleep(200 * time.Millisecond)
			continue
		}

		if err := ws.WriteMessage(rweb.BinaryMessage, frame); err != nil {
			return nil
		}

		time.Sleep(100 * time.Millisecond)
	}
}
