package views

import (
	"camctl/storage"
	_ "embed"
	"fmt"
	"strings"

	"github.com/rohanthewiz/element"
)

//go:embed styles.css
var cssStyles string

//go:embed app.js
var jsScript string

// jsEscape escapes a string for safe use inside a single-quoted JS string literal.
// The element builder does not HTML-escape attribute values, so we must avoid
// double quotes (which would break onclick="..." attributes) and instead use
// single-quoted JS strings with backslash-escaped single quotes.
func jsEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return s
}

// Settings holds the current camera connection state for rendering.
type Settings struct {
	CameraLabel string
	CameraIP    string
	CameraPort  int
	Connected   bool
}

// CameraItem is a saved camera entry passed from handlers to the view.
type CameraItem struct {
	Label string
	IP    string
	Port  int
}

// PageData bundles everything the main page template needs.
type PageData struct {
	Settings        Settings
	Presets         []storage.Preset
	Cameras         []CameraItem
	PreviewSettings storage.PreviewSettings
}

// RenderPage produces the full HTML page: D-pad, zoom indicator, presets, and settings.
func RenderPage(data PageData) string {
	b := element.NewBuilder()

	b.Html("lang", "en").R(
		b.Head().R(
			b.Meta("charset", "utf-8").R(),
			b.Meta("name", "viewport", "content", "width=device-width, initial-scale=1").R(),
			b.Title().T("CamCtl"),
			b.Style().T(cssStyles),
		),
		b.Body().R(
			b.DivClass("container").R(
				// Header
				b.H1().T("CamCtl"),
				b.PClass("subtitle").T("NDI Camera Control"),

				// Connection status indicator
				StatusView{Settings: data.Settings}.Render(b),

				// Two-column layout: left = D-pad + zoom, right = presets + settings
				b.DivClass("columns").R(
					// Left column — movement controls
					b.DivClass("col").R(
						// NDI preview — shows live camera feed via WebSocket
						PreviewView{PreviewSettings: data.PreviewSettings}.Render(b),
						// Active camera indicator — prominently shows which camera is under control
						ActiveCameraView{Settings: data.Settings}.Render(b),
						DPadView{}.Render(b),
						ZoomSectionView{}.Render(b),
					),

					// Right column — cameras (primary), then presets
					b.DivClass("col").R(
						CamerasView{Settings: data.Settings, Cameras: data.Cameras}.Render(b),
						PresetsView{Presets: data.Presets}.Render(b),
					),
				),
			),
			// Toast container for error/status notifications
			b.Div("id", "toast-container", "class", "toast-container").R(),
			// Edit camera modal
			b.Div("id", "edit-modal", "class", "modal-overlay", "onclick", "closeEditModal(event)").R(
				b.DivClass("modal").R(
					b.H2().T("Edit Camera"),
					b.Input("type", "hidden", "id", "edit-old-label").R(),
					b.DivClass("settings-row").R(
						b.Label("for", "edit-label").T("Name"),
						b.Input("type", "text", "id", "edit-label").R(),
					),
					b.DivClass("settings-row").R(
						b.Label("for", "edit-ip").T("IP Address"),
						b.Input("type", "text", "id", "edit-ip").R(),
					),
					b.DivClass("settings-row").R(
						b.Label("for", "edit-port").T("Port"),
						b.Input("type", "number", "id", "edit-port").R(),
					),
					b.DivClass("modal-actions").R(
						b.Button("class", "modal-btn cancel", "onclick", "closeEditModal()").T("Cancel"),
						b.Button("class", "modal-btn save", "onclick", "saveEditCamera()").T("Save"),
					),
				),
			),
			b.Script().T(jsScript),
		),
	)
	return b.String()
}

// StatusView shows a connection status badge.
// Implements element.Component so it can be used directly as an R() argument.
type StatusView struct {
	Settings Settings
}

func (v StatusView) Render(b *element.Builder) (x any) {
	s := v.Settings
	statusClass := "status disconnected"
	statusText := "Disconnected"
	if s.Connected {
		statusClass = "status connected"
		label := s.CameraLabel
		if label == "" {
			label = s.CameraIP
		}
		statusText = fmt.Sprintf("Connected — %s (%s:%d)", label, s.CameraIP, s.CameraPort)
	}
	b.Div("id", "conn-status", "class", statusClass).T(statusText)
	return
}

// ActiveCameraView shows which camera is currently being controlled,
// displayed prominently above the D-pad so the operator always knows the target.
// Implements element.Component so it can be used directly as an R() argument.
type ActiveCameraView struct {
	Settings Settings
}

func (v ActiveCameraView) Render(b *element.Builder) (x any) {
	s := v.Settings
	if s.Connected {
		label := s.CameraLabel
		if label == "" {
			label = s.CameraIP
		}
		b.Div("id", "active-camera", "class", "active-camera connected").R(
			b.Span("class", "active-dot").R(),
			b.Span("class", "active-label").T(label),
		)
	} else {
		b.Div("id", "active-camera", "class", "active-camera disconnected").R(
			b.Span("class", "active-label").T("No camera connected"),
		)
	}
	return
}

// PreviewView creates the live preview box with a gear icon to toggle
// a collapsible settings panel for enabling/disabling preview protocols.
// Implements element.Component so it can be used directly as an R() argument.
type PreviewView struct {
	PreviewSettings storage.PreviewSettings
}

func (v PreviewView) Render(b *element.Builder) (x any) {
	ps := v.PreviewSettings

	// Helper to conditionally add "checked" attribute for checkboxes.
	chk := func(enabled bool) []string {
		if enabled {
			return []string{"type", "checkbox", "checked", "checked"}
		}
		return []string{"type", "checkbox"}
	}

	b.DivClass("preview-container").R(
		// Preview box with gear icon overlay
		b.DivClass("preview-box").R(
			b.Img("id", "preview-img", "alt", "").R(),
			b.Div("id", "preview-no-signal", "class", "preview-no-signal").T("No Signal"),
			b.Button("class", "preview-gear-btn", "onclick", "togglePreviewSettings()", "title", "Preview settings").T("\u2699"),
		),

		// Collapsible settings panel — hidden by default, toggled by gear icon
		b.Div("id", "preview-settings-panel", "class", "preview-settings-panel").R(
			b.H3().T("Preview Source"),

			// NDI Direct checkbox
			b.DivClass("preview-opt").R(
				b.Label("title", "Requires NDI SDK (libndi) installed on this machine").R(
					b.Input(append(chk(ps.EnableNDI), "id", "pv-ndi")...).R(),
					b.Span().T(" NDI Direct"),
				),
			),

			// OBS WebSocket checkbox
			b.DivClass("preview-opt").R(
				b.Label("title", "Requires OBS Studio running with WebSocket server enabled (Tools \u2192 WebSocket Server Settings)").R(
					b.Input(append(chk(ps.EnableOBS), "id", "pv-obs")...).R(),
					b.Span().T(" OBS WebSocket"),
				),
			),

			// OBS connection fields — indented below the OBS checkbox
			b.DivClass("preview-obs-fields").R(
				b.DivClass("settings-row").R(
					b.Label("for", "pv-obs-host").T("Host"),
					b.Input("type", "text", "id", "pv-obs-host",
						"placeholder", "localhost:4455",
						"value", ps.OBSWSHost).R(),
				),
				b.DivClass("settings-row").R(
					b.Label("for", "pv-obs-password").T("Password"),
					b.Input("type", "password", "id", "pv-obs-password",
						"placeholder", "OBS WebSocket password",
						"value", ps.OBSWSPassword).R(),
				),
			),

			// HTTP Snapshot checkbox
			b.DivClass("preview-opt").R(
				b.Label("title", "Probes the camera IP for JPEG snapshot endpoints (no extra software needed)").R(
					b.Input(append(chk(ps.EnableHTTP), "id", "pv-http")...).R(),
					b.Span().T(" HTTP Snapshot"),
				),
			),

			// RTSP (ffmpeg) checkbox
			b.DivClass("preview-opt").R(
				b.Label("title", "Requires ffmpeg installed and camera RTSP stream accessible").R(
					b.Input(append(chk(ps.EnableRTSP), "id", "pv-rtsp")...).R(),
					b.Span().T(" RTSP (ffmpeg)"),
				),
			),

			// Save button
			b.Button("class", "connect-btn", "onclick", "savePreviewSettings()").T("Save & Reconnect"),
		),
	)
	return
}

// DPadView creates the 3x3 directional pad grid.
// Implements element.Component so it can be used directly as an R() argument.
//
//	Layout:
//	  [     ] [ UP  ] [     ]
//	  [LEFT ] [HOME ] [RIGHT]
//	  [     ] [DOWN ] [     ]
type DPadView struct{}

func (v DPadView) Render(b *element.Builder) (x any) {
	b.DivClass("section").R(
		b.H2().T("Pan / Tilt"),
		b.PClass("dpad-hint").T("Press and hold for larger movements"),
		b.DivClass("dpad").R(
			// Row 1
			b.DivClass("dpad-cell empty").R(),
			DPadButtonView{Direction: "up", Label: "UP", ID: "btn-up"}.Render(b),
			b.DivClass("dpad-cell empty").R(),
			// Row 2
			DPadButtonView{Direction: "left", Label: "LEFT", ID: "btn-left"}.Render(b),
			DPadButtonView{Direction: "home", Label: "HOME", ID: "btn-home"}.Render(b),
			DPadButtonView{Direction: "right", Label: "RIGHT", ID: "btn-right"}.Render(b),
			// Row 3
			b.DivClass("dpad-cell empty").R(),
			DPadButtonView{Direction: "down", Label: "DOWN", ID: "btn-down"}.Render(b),
			b.DivClass("dpad-cell empty").R(),
		),
	)
	return
}

// DPadButtonView creates a single D-pad button that starts ramped movement on press
// and stops on release. Home is a one-shot command (no ramping needed).
// Implements element.Component so it can be used directly as an R() argument.
type DPadButtonView struct {
	Direction string
	Label     string
	ID        string
}

func (v DPadButtonView) Render(b *element.Builder) (x any) {
	if v.Direction == "home" {
		b.Button("id", v.ID, "class", "dpad-cell dpad-btn",
			"onmousedown", fmt.Sprintf("sendMove('%s')", v.Direction),
			"ontouchstart", fmt.Sprintf("sendMove('%s'); event.preventDefault()", v.Direction),
		).T(v.Label)
	} else {
		// Directional buttons use startMove/stopMove for speed curve ramping.
		// startMove begins at the curve's initial speed and ramps over time;
		// stopMove clears the ramp interval and sends a VISCA stop command.
		b.Button("id", v.ID, "class", "dpad-cell dpad-btn",
			"onmousedown", fmt.Sprintf("startMove('%s')", v.Direction),
			"onmouseup", "stopMove()",
			"onmouseleave", "stopMove()",
			"ontouchstart", fmt.Sprintf("startMove('%s'); event.preventDefault()", v.Direction),
			"ontouchend", "stopMove()",
		).T(v.Label)
	}
	return
}

// ZoomSectionView creates the zoom indicator with a gear icon that toggles
// the advanced zoom controls (speed curve selector + speed slider).
// The zoom heading, hint, and bar are always visible; the advanced controls
// are hidden by default and revealed by clicking the gear.
// Implements element.Component so it can be used directly as an R() argument.
type ZoomSectionView struct{}

func (v ZoomSectionView) Render(b *element.Builder) (x any) {
	b.DivClass("section zoom-section").R(
		// Zoom heading row with gear icon for advanced settings
		b.DivClass("zoom-header").R(
			b.H2().T("Zoom"),
			b.Button("class", "zoom-gear-btn", "onclick", "toggleZoomSettings()", "title", "Zoom settings").T("\u2699"),
		),
		b.PClass("zoom-hint").T("Scroll wheel to zoom in / out"),
		b.DivClass("zoom-bar").R(
			b.Div("id", "zoom-indicator", "class", "zoom-level").R(),
		),

		// Collapsible advanced zoom controls — hidden by default, toggled by gear icon
		b.Div("id", "zoom-settings-panel", "class", "zoom-settings-panel").R(
			// Curve selector — three toggle buttons in a horizontal group
			b.DivClass("curve-selector").R(
				b.Button("id", "curve-constant", "class", "curve-btn active",
					"onclick", "setCurve('constant')").T("Constant"),
				b.Button("id", "curve-linear", "class", "curve-btn",
					"onclick", "setCurve('linear')").T("Linear"),
				b.Button("id", "curve-expo", "class", "curve-btn",
					"onclick", "setCurve('expo')").T("Expo"),
			),

			// Speed slider with numeric readout
			b.DivClass("speed-slider-row").R(
				b.SpanClass("speed-label").T("Speed"),
				b.Input("type", "range", "id", "speed-slider",
					"min", "1", "max", "24", "value", "8",
					"class", "speed-slider",
					"oninput", "updateSpeedDisplay(this.value)").R(),
				b.Span("id", "speed-value", "class", "speed-value").T("8"),
			),
		),
	)
	return
}

// PresetsView creates 6 presets in a 3-per-row grid.
// Each preset card has an editable label, a green GO button, and a muted Save button.
// Implements element.Component so it can be used directly as an R() argument.
type PresetsView struct {
	Presets []storage.Preset
}

func (v PresetsView) Render(b *element.Builder) (x any) {
	b.DivClass("section").R(
		b.H2().T("Presets"),
		b.DivClass("preset-grid").R(
			b.Wrap(func() {
				for _, p := range v.Presets {
					// Presets with user-assigned labels get a highlight color;
					// default "Preset N" labels are dimmed to visually separate
					// configured slots from empty ones.
					labelClass := "preset-label"
					if p.Label != fmt.Sprintf("Preset %d", p.Number+1) {
						labelClass = "preset-label set"
					}
					b.DivClass("preset-card").R(
						b.Input("type", "text", "class", labelClass,
							"id", fmt.Sprintf("preset-label-%d", p.Number),
							"value", p.Label,
							"onchange", fmt.Sprintf("saveLabel(%d, this.value)", p.Number),
						).R(),
						b.DivClass("preset-actions").R(
							b.Button("class", "preset-btn go",
								"onclick", fmt.Sprintf("presetRecall(%d)", p.Number),
							).T("GO"),
							b.Button("class", "preset-btn save",
								"onclick", fmt.Sprintf("presetSet(%d)", p.Number),
							).T("Save"),
						),
					)
				}
			}),
		),
	)
	return
}

// CamerasView creates the camera management section: saved cameras list
// at the top for one-click switching, with a collapsible "Add Camera" form below.
// When no cameras are saved, the form is shown by default.
// Implements element.Component so it can be used directly as an R() argument.
type CamerasView struct {
	Settings Settings
	Cameras  []CameraItem
}

func (v CamerasView) Render(b *element.Builder) (x any) {
	s := v.Settings
	cams := v.Cameras
	hasCameras := len(cams) > 0

	// Show the add form by default when there are no saved cameras
	formClass := "add-camera-form"
	if !hasCameras {
		formClass = "add-camera-form open"
	}

	b.DivClass("section cameras-section").R(
		b.H2().T("Cameras"),

		// Saved cameras list — one-click to connect
		b.Wrap(func() {
			if hasCameras {
				b.DivClass("saved-cameras").R(
					b.Wrap(func() {
						for _, cam := range cams {
							isActive := cam.IP == s.CameraIP && cam.Port == s.CameraPort && s.Connected
							cardClass := "camera-item"
							if isActive {
								cardClass = "camera-item active"
							}
							portStr := fmt.Sprintf("%d", cam.Port)
							escLabel := jsEscape(cam.Label)
							escIP := jsEscape(cam.IP)
							onclick := fmt.Sprintf("connectCamera('%s','%s','%s')", escLabel, escIP, portStr)
							reconnectClick := fmt.Sprintf("reconnectCamera('%s','%s','%s',event)", escLabel, escIP, portStr)
							editClick := fmt.Sprintf("editCamera('%s','%s','%s',event)", escLabel, escIP, portStr)
							removeClick := fmt.Sprintf("removeCamera('%s',event)", escLabel)
							b.Div("class", cardClass, "onclick", onclick).R(
								b.DivClass("camera-item-info").R(
									b.Span("class", "camera-name").T(cam.Label),
									b.Span("class", "camera-addr").T(fmt.Sprintf("%s:%d", cam.IP, cam.Port)),
								),
								b.DivClass("camera-actions").R(
									b.Button("class", "camera-action-btn reconnect", "onclick", reconnectClick, "title", "Reconnect").T("\u21bb"),
									b.Button("class", "camera-action-btn edit", "onclick", editClick, "title", "Edit").T("\u270e"),
									b.Button("class", "camera-action-btn delete", "onclick", removeClick, "title", "Delete").T("\u00d7"),
								),
							)
						}
					}),
				)
			} else {
				b.PClass("no-cameras-hint").T("No cameras saved yet. Add one to get started.")
			}
		}),

		// Toggle button to show/hide the add-camera form
		b.Wrap(func() {
			if hasCameras {
				b.Button("class", "add-camera-toggle", "onclick", "toggleAddForm()").R(
					b.Span("class", "add-camera-toggle-icon").T("+"),
					b.Span().T("Add Camera"),
				)
			}
		}),

		// Add camera form — collapsible
		b.Div("id", "add-camera-form", "class", formClass).R(
			b.DivClass("settings-row").R(
				b.Label("for", "camera-label").T("Name"),
				b.Input("type", "text", "id", "camera-label",
					"placeholder", "e.g. Stage Left").R(),
			),
			b.DivClass("settings-row").R(
				b.Label("for", "camera-ip").T("IP Address"),
				b.Input("type", "text", "id", "camera-ip",
					"placeholder", "192.168.1.100").R(),
			),
			// Port row — hidden by default, toggled via "Advanced" link
			b.DivClass("advanced-toggle").R(
				b.A("href", "#", "onclick", "toggleAdvanced(event)").T("Advanced settings"),
			),
			b.Div("id", "advanced-fields", "class", "advanced-fields").R(
				b.DivClass("settings-row").R(
					b.Label("for", "camera-port").T("Port"),
					b.Input("type", "number", "id", "camera-port",
						"value", "52381", "placeholder", "52381").R(),
				),
			),
			b.DivClass("settings-row").R(
				b.Button("id", "connect-btn", "class", "connect-btn",
					"onclick", "addCamera()",
				).T("Add & Connect"),
			),
		),
	)
	return
}
