package handlers

import (
	"camctl/cameras"
	"camctl/presets"
	"camctl/views"
	"camctl/visca"
	"fmt"
	"strconv"
	"sync"

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
// preset store, camera store, and current camera settings.
// The mutex protects the VISCA client during reconnection.
type App struct {
	mu       sync.RWMutex
	Camera   *visca.Client
	Presets  *presets.Store
	Cameras  *cameras.Store
	Settings views.Settings
}

// NewApp creates an App with the given preset and camera stores.
// The VISCA client starts disconnected — the user connects via the settings UI.
func NewApp(presetStore *presets.Store, cameraStore *cameras.Store) *App {
	return &App{
		Presets: presetStore,
		Cameras: cameraStore,
		Settings: views.Settings{
			CameraPort: visca.DefaultPort,
		},
	}
}

// RegisterRoutes wires all handlers to the rweb server.
func (a *App) RegisterRoutes(s *rweb.Server) {
	s.Get("/", a.handleIndex)
	s.Post("/api/move", a.handleMove)
	s.Post("/api/zoom", a.handleZoom)
	s.Post("/api/preset/recall", a.handlePresetRecall)
	s.Post("/api/preset/set", a.handlePresetSet)
	s.Post("/api/preset/label", a.handlePresetLabel)
	s.Post("/api/settings", a.handleSettings)
	s.Post("/api/camera/remove", a.handleCameraRemove)
}

// handleIndex renders the full page with current settings, presets, and saved cameras.
func (a *App) handleIndex(c rweb.Context) error {
	a.mu.RLock()
	settings := a.Settings
	a.mu.RUnlock()

	rawCams := a.Cameras.All()
	camItems := make([]views.CameraItem, len(rawCams))
	for i, cam := range rawCams {
		camItems[i] = views.CameraItem{Label: cam.Label, IP: cam.IP, Port: cam.Port}
	}

	data := views.PageData{
		Settings: settings,
		Presets:  a.Presets.All(),
		Cameras:  camItems,
	}
	return c.WriteHTML(views.RenderPage(data))
}

// handleMove processes pan/tilt/home/stop commands.
// Expects form param "direction": left, right, up, down, home, stop.
// Optional "panSpeed" (1–24) and "tiltSpeed" (1–23) override the defaults,
// enabling client-side speed curves to ramp movement over time.
func (a *App) handleMove(c rweb.Context) error {
	direction := c.Request().FormValue("direction")

	// Parse optional speed overrides — the JS ramping logic sends these
	// with increasing values while a D-pad button is held down.
	panSpeed := defaultPanSpeed
	tiltSpeed := defaultTiltSpeed
	if ps := c.Request().FormValue("panSpeed"); ps != "" {
		if v, err := strconv.Atoi(ps); err == nil && v >= 1 && v <= 0x18 {
			panSpeed = byte(v)
		}
	}
	if ts := c.Request().FormValue("tiltSpeed"); ts != "" {
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
	action := c.Request().FormValue("action")
	speedStr := c.Request().FormValue("speed")

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
	num, err := strconv.Atoi(c.Request().FormValue("num"))
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
	num, err := strconv.Atoi(c.Request().FormValue("num"))
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

// handlePresetLabel updates a preset's display label and persists it to disk.
// Expects form params "num" (0–5) and "label" (text).
func (a *App) handlePresetLabel(c rweb.Context) error {
	num, err := strconv.Atoi(c.Request().FormValue("num"))
	if err != nil || num < 0 || num > 5 {
		return c.WriteJSON(map[string]string{"error": "invalid preset number"})
	}

	label := c.Request().FormValue("label")
	if err := a.Presets.UpdateLabel(num, label); err != nil {
		return serr.Wrap(err, "preset label update failed")
	}
	return c.WriteJSON(map[string]string{"status": "ok"})
}

// cameraJSON represents a saved camera in the settings response.
type cameraJSON struct {
	Label string `json:"label"`
	IP    string `json:"ip"`
	Port  int    `json:"port"`
}

// settingsResponse is the JSON shape returned after a connection attempt.
// Includes the full saved-cameras list so the client can update the sidebar
// without a page reload.
type settingsResponse struct {
	Connected bool         `json:"connected"`
	Label     string       `json:"label,omitempty"`
	IP        string       `json:"ip,omitempty"`
	Port      int          `json:"port,omitempty"`
	Error     string       `json:"error,omitempty"`
	Cameras   []cameraJSON `json:"cameras"`
}

// handleSettings connects to a camera and saves it to the camera list when a
// label is provided. Used for both one-click saved-camera connections and
// new camera additions from the add-camera form.
// Expects form params "label" (required for saving), "ip", and "port".
func (a *App) handleSettings(c rweb.Context) error {
	label := c.Request().FormValue("label")
	ip := c.Request().FormValue("ip")
	portStr := c.Request().FormValue("port")

	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		port = visca.DefaultPort
	}

	a.mu.Lock()
	// Tear down any existing connection before reconnecting.
	if a.Camera != nil {
		_ = a.Camera.Close()
	}

	cam := visca.NewClient(ip, port)
	connectErr := cam.Connect()

	if connectErr != nil {
		a.Camera = nil
		a.Settings = views.Settings{CameraLabel: label, CameraIP: ip, CameraPort: port, Connected: false}
		a.mu.Unlock()

		return c.WriteJSON(settingsResponse{
			Connected: false,
			Error:     connectErr.Error(),
			Cameras:   a.savedCamerasJSON(),
		})
	}

	a.Camera = cam
	a.Settings = views.Settings{CameraLabel: label, CameraIP: ip, CameraPort: port, Connected: true}
	a.mu.Unlock()

	// Persist the camera to the saved list when a label is provided.
	if label != "" {
		_ = a.Cameras.Upsert(cameras.Camera{Label: label, IP: ip, Port: port})
	}

	return c.WriteJSON(settingsResponse{
		Connected: true,
		Label:     label,
		IP:        ip,
		Port:      port,
		Cameras:   a.savedCamerasJSON(),
	})
}

// savedCamerasJSON converts the camera store into the JSON response format.
func (a *App) savedCamerasJSON() []cameraJSON {
	raw := a.Cameras.All()
	out := make([]cameraJSON, len(raw))
	for i, cam := range raw {
		out[i] = cameraJSON{Label: cam.Label, IP: cam.IP, Port: cam.Port}
	}
	return out
}

// handleCameraRemove removes a camera from the saved list.
// Expects form param "label".
func (a *App) handleCameraRemove(c rweb.Context) error {
	label := c.Request().FormValue("label")
	if err := a.Cameras.Remove(label); err != nil {
		return serr.Wrap(err, "camera remove failed")
	}
	return c.WriteJSON(map[string]string{"status": "ok"})
}
