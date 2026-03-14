package views

import (
	"camctl/storage"
	"fmt"
	"strings"

	"github.com/rohanthewiz/element"
)

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
	Settings Settings
	Presets  []storage.Preset
	Cameras  []CameraItem
}

// RenderPage produces the full HTML page: D-pad, zoom indicator, presets, and settings.
func RenderPage(data PageData) string {
	b := element.NewBuilder()

	b.Html("lang", "en").R(
		b.Head().R(
			b.Meta("charset", "utf-8").R(),
			b.Meta("name", "viewport", "content", "width=device-width, initial-scale=1").R(),
			b.Title().T("CamCtl"),
			b.Style().T(cssStyles()),
		),
		b.Body().R(
			b.DivClass("container").R(
				// Header
				b.H1().T("CamCtl"),
				b.PClass("subtitle").T("NDI Camera Control"),

				// Connection status indicator
				renderStatus(b, data.Settings),

				// Two-column layout: left = D-pad + zoom, right = presets + settings
				b.DivClass("columns").R(
					// Left column — movement controls
					b.DivClass("col").R(
						// NDI preview — shows live camera feed via WebSocket
						renderPreview(b),
						// Active camera indicator — prominently shows which camera is under control
						renderActiveCamera(b, data.Settings),
						renderDPad(b),
						renderSpeedControls(b),

						// Zoom indicator
						b.DivClass("section").R(
							b.H2().T("Zoom"),
							b.PClass("zoom-hint").T("Scroll wheel to zoom in / out"),
							b.DivClass("zoom-bar").R(
								b.Div("id", "zoom-indicator", "class", "zoom-level").R(),
							),
						),
					),

					// Right column — cameras (primary), then presets
					b.DivClass("col").R(
						renderCameras(b, data.Settings, data.Cameras),
						renderPresets(b, data.Presets),
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
			b.Script().T(jsScript()),
		),
	)
	return b.String()
}

// renderStatus shows a connection status badge.
func renderStatus(b *element.Builder, s Settings) *element.Builder {
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
	return b
}

// renderActiveCamera shows which camera is currently being controlled,
// displayed prominently above the D-pad so the operator always knows the target.
func renderActiveCamera(b *element.Builder, s Settings) *element.Builder {
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
	return b
}

// renderPreview creates the NDI live preview box.
func renderPreview(b *element.Builder) *element.Builder {
	b.DivClass("preview-box").R(
		b.Img("id", "preview-img", "alt", "").R(),
		b.Div("id", "preview-no-signal", "class", "preview-no-signal").T("No Signal"),
	)
	return b
}

// renderDPad creates the 3x3 directional pad grid.
//
//	Layout:
//	  [     ] [ UP  ] [     ]
//	  [LEFT ] [HOME ] [RIGHT]
//	  [     ] [DOWN ] [     ]
func renderDPad(b *element.Builder) *element.Builder {
	b.DivClass("section").R(
		b.H2().T("Pan / Tilt"),
		b.PClass("dpad-hint").T("Press and hold for larger movements"),
		b.DivClass("dpad").R(
			// Row 1
			b.DivClass("dpad-cell empty").R(),
			dpadButton(b, "up", "UP", "btn-up"),
			b.DivClass("dpad-cell empty").R(),
			// Row 2
			dpadButton(b, "left", "LEFT", "btn-left"),
			dpadButton(b, "home", "HOME", "btn-home"),
			dpadButton(b, "right", "RIGHT", "btn-right"),
			// Row 3
			b.DivClass("dpad-cell empty").R(),
			dpadButton(b, "down", "DOWN", "btn-down"),
			b.DivClass("dpad-cell empty").R(),
		),
	)
	return b
}

// dpadButton creates a single D-pad button that starts ramped movement on press
// and stops on release. Home is a one-shot command (no ramping needed).
func dpadButton(b *element.Builder, direction, label, id string) *element.Builder {
	if direction == "home" {
		b.Button("id", id, "class", "dpad-cell dpad-btn",
			"onmousedown", fmt.Sprintf("sendMove('%s')", direction),
			"ontouchstart", fmt.Sprintf("sendMove('%s'); event.preventDefault()", direction),
		).T(label)
	} else {
		// Directional buttons use startMove/stopMove for speed curve ramping.
		// startMove begins at the curve's initial speed and ramps over time;
		// stopMove clears the ramp interval and sends a VISCA stop command.
		b.Button("id", id, "class", "dpad-cell dpad-btn",
			"onmousedown", fmt.Sprintf("startMove('%s')", direction),
			"onmouseup", "stopMove()",
			"onmouseleave", "stopMove()",
			"ontouchstart", fmt.Sprintf("startMove('%s'); event.preventDefault()", direction),
			"ontouchend", "stopMove()",
		).T(label)
	}
	return b
}

// renderSpeedControls creates the speed curve selector and max-speed slider.
// Placed below the D-pad so the operator can adjust how hold duration
// maps to movement speed.
//
//	Curve selector: three mutually-exclusive buttons (Constant / Linear / Expo)
//	Speed slider:   sets the target max speed (1–24) for the selected curve
func renderSpeedControls(b *element.Builder) *element.Builder {
	b.DivClass("section speed-controls").R(
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
	)
	return b
}

// renderPresets creates 6 presets in a 3-per-row grid.
// Each preset card has an editable label, a green GO button, and a muted Save button.
func renderPresets(b *element.Builder, prs []storage.Preset) *element.Builder {
	b.DivClass("section").R(
		b.H2().T("Presets"),
		b.DivClass("preset-grid").R(
			b.Wrap(func() {
				for _, p := range prs {
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
	return b
}

// renderCameras creates the camera management section: saved cameras list
// at the top for one-click switching, with a collapsible "Add Camera" form below.
// When no cameras are saved, the form is shown by default.
func renderCameras(b *element.Builder, s Settings, cams []CameraItem) *element.Builder {
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
	return b
}

// cssStyles returns the full CSS for a dark-themed, touch-friendly remote control UI.
func cssStyles() string {
	return `
* { box-sizing: border-box; margin: 0; padding: 0; }

body {
	background: #1a1a2e;
	color: #e0e0e0;
	font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
	display: flex;
	justify-content: center;
	min-height: 100vh;
	padding: 20px;
}

.container {
	max-width: 900px;
	width: 100%;
}

/* ---- Two-column layout ---- */
.columns {
	display: grid;
	grid-template-columns: 1fr 1fr;
	gap: 32px;
	align-items: start;
}

.col {
	min-width: 0;
}

/* Stack columns on narrow screens */
@media (max-width: 640px) {
	.columns {
		grid-template-columns: 1fr;
		gap: 16px;
	}
}

h1 {
	text-align: center;
	font-size: 1.8rem;
	color: #f97316;
	margin-bottom: 4px;
}

.subtitle {
	text-align: center;
	color: #888;
	font-size: 0.9rem;
	margin-bottom: 16px;
}

/* Connection status badge */
.status {
	text-align: center;
	padding: 6px 12px;
	border-radius: 20px;
	font-size: 0.8rem;
	margin-bottom: 20px;
}
.connected    { background: #0f3d0f; color: #4caf50; border: 1px solid #4caf50; }
.disconnected { background: #3d1a00; color: #f97316; border: 1px solid #f97316; }

.section {
	margin-bottom: 24px;
}

h2 {
	font-size: 1rem;
	color: #aaa;
	text-transform: uppercase;
	letter-spacing: 1px;
	margin-bottom: 12px;
}

/* ---- D-Pad: 3x3 CSS grid ---- */
.dpad {
	display: grid;
	grid-template-columns: repeat(3, 1fr);
	gap: 8px;
	max-width: 320px;
	margin: 0 auto;
}

.dpad-cell {
	aspect-ratio: 1;
	display: flex;
	align-items: center;
	justify-content: center;
	border-radius: 12px;
	font-size: 1rem;
	font-weight: 700;
}

.dpad-cell.empty {
	background: transparent;
}

.dpad-btn {
	background: #16213e;
	color: #e0e0e0;
	border: 2px solid #0f3460;
	cursor: pointer;
	user-select: none;
	-webkit-user-select: none;
	transition: background 0.1s, transform 0.1s;
}

.dpad-btn:active {
	background: #f97316;
	color: #fff;
	transform: scale(0.95);
}

#btn-home {
	background: #0f3460;
	border-color: #f97316;
}

/* ---- Speed curve controls ---- */
.speed-controls {
	margin-bottom: 8px;
}

/* Curve selector: horizontal toggle-button group */
.curve-selector {
	display: flex;
	gap: 4px;
	margin-bottom: 10px;
}
.curve-btn {
	flex: 1;
	padding: 6px 0;
	background: #16213e;
	color: #888;
	border: 1px solid #0f3460;
	border-radius: 6px;
	cursor: pointer;
	font-size: 0.8rem;
	font-weight: 600;
	transition: background 0.15s, color 0.15s, border-color 0.15s;
}
.curve-btn:hover {
	color: #bbb;
	border-color: #1a4a80;
}
.curve-btn.active {
	background: #f97316;
	color: #fff;
	border-color: #f97316;
}

/* Speed slider row: label — range input — numeric readout */
.speed-slider-row {
	display: flex;
	align-items: center;
	gap: 10px;
}
.speed-label {
	font-size: 0.85rem;
	color: #aaa;
	min-width: 40px;
}
.speed-slider {
	flex: 1;
	-webkit-appearance: none;
	appearance: none;
	height: 6px;
	background: #16213e;
	border-radius: 3px;
	outline: none;
}
.speed-slider::-webkit-slider-thumb {
	-webkit-appearance: none;
	width: 18px;
	height: 18px;
	border-radius: 50%;
	background: #f97316;
	cursor: pointer;
	border: 2px solid #1a1a2e;
}
.speed-slider::-moz-range-thumb {
	width: 18px;
	height: 18px;
	border-radius: 50%;
	background: #f97316;
	cursor: pointer;
	border: 2px solid #1a1a2e;
}
.speed-value {
	font-size: 0.85rem;
	color: #f97316;
	font-weight: 700;
	min-width: 24px;
	text-align: center;
}

/* ---- Zoom ---- */
.dpad-hint, .zoom-hint {
	color: #666;
	font-size: 0.85rem;
	margin-bottom: 8px;
}

.zoom-bar {
	height: 6px;
	background: #16213e;
	border-radius: 3px;
	overflow: hidden;
}

.zoom-level {
	height: 100%;
	width: 50%;
	background: #f97316;
	border-radius: 3px;
	transition: width 0.15s;
}

/* ---- Presets: 3-per-row grid ---- */
.preset-grid {
	display: grid;
	grid-template-columns: repeat(3, 1fr);
	gap: 8px;
}

.preset-card {
	background: #16213e;
	border: 1px solid #0f3460;
	border-radius: 8px;
	padding: 8px;
	display: flex;
	flex-direction: column;
	gap: 6px;
}

.preset-label {
	width: 100%;
	background: #1a1a2e;
	border: 1px solid #0f3460;
	color: #666;
	padding: 6px 8px;
	border-radius: 4px;
	font-size: 0.85rem;
	text-align: center;
}

/* Presets with user-assigned labels stand out in green-yellow */
.preset-label.set {
	color: #a3e635;
}

.preset-actions {
	display: flex;
	gap: 4px;
}

.preset-btn {
	flex: 1;
	padding: 6px 0;
	border: none;
	border-radius: 4px;
	cursor: pointer;
	font-weight: 700;
	font-size: 0.85rem;
}

/* GO button — vivid green, the main action */
.preset-btn.go {
	background: #166534;
	color: #4ade80;
	border: 1px solid #22c55e;
	letter-spacing: 0.05em;
}
.preset-btn.go:hover  { background: #15803d; }
.preset-btn.go:active { background: #16a34a; color: #fff; }

/* Save button — intentionally muted so it's not clicked accidentally */
.preset-btn.save {
	background: #1e1b2e;
	color: #555;
	border: 1px solid #2a2740;
	font-weight: 500;
}
.preset-btn.save:hover  { color: #888; border-color: #444; }
.preset-btn.save:active { background: #2a2740; color: #aaa; }

/* ---- Cameras section ---- */
.cameras-section {
	margin-bottom: 24px;
}

.no-cameras-hint {
	color: #666;
	font-size: 0.85rem;
	margin-bottom: 12px;
	font-style: italic;
}

/* Saved cameras list */
.saved-cameras {
	display: flex;
	flex-direction: column;
	gap: 6px;
	margin-bottom: 10px;
}

.camera-item {
	display: flex;
	align-items: center;
	justify-content: space-between;
	gap: 8px;
	background: #16213e;
	border: 1px solid #0f3460;
	border-radius: 6px;
	padding: 10px 12px;
	cursor: pointer;
	transition: border-color 0.15s, background 0.15s;
}
.camera-item:hover  { background: #1c2a50; border-color: #1a4a80; }
.camera-item.active { border-color: #4caf50; background: #0d2b0d; }

.camera-item-info {
	display: flex;
	flex-direction: column;
	gap: 2px;
	min-width: 0;
}

.camera-name {
	font-size: 0.9rem;
	font-weight: 600;
	color: #e0e0e0;
	white-space: nowrap;
	overflow: hidden;
	text-overflow: ellipsis;
}

.camera-addr {
	font-size: 0.75rem;
	color: #666;
}

.camera-item.active .camera-name { color: #4caf50; }
.camera-item.active .camera-addr { color: #2d7a2d; }

.camera-actions {
	display: flex;
	gap: 4px;
	flex-shrink: 0;
}

.camera-action-btn {
	background: transparent;
	border: none;
	color: #555;
	font-size: 1rem;
	cursor: pointer;
	padding: 4px 6px;
	border-radius: 4px;
	line-height: 1;
	transition: color 0.15s, background 0.15s;
}
.camera-action-btn:hover { background: #1a1a2e; }
.camera-action-btn.reconnect:hover { color: #4caf50; }
.camera-action-btn.edit:hover { color: #60a5fa; }
.camera-action-btn.delete:hover { color: #ef4444; }

/* Edit camera modal */
.modal-overlay {
	display: none;
	position: fixed;
	inset: 0;
	background: rgba(0,0,0,0.6);
	z-index: 2000;
	align-items: center;
	justify-content: center;
}
.modal-overlay.open {
	display: flex;
}
.modal {
	background: #1a1a2e;
	border: 1px solid #0f3460;
	border-radius: 12px;
	padding: 24px;
	width: 100%;
	max-width: 380px;
}
.modal h2 {
	margin-bottom: 16px;
	color: #e0e0e0;
	text-transform: none;
	font-size: 1.1rem;
}
.modal-actions {
	display: flex;
	gap: 8px;
	margin-top: 8px;
}
.modal-btn {
	flex: 1;
	padding: 8px;
	border: none;
	border-radius: 6px;
	font-size: 0.9rem;
	font-weight: 600;
	cursor: pointer;
}
.modal-btn.cancel {
	background: #16213e;
	color: #888;
	border: 1px solid #0f3460;
}
.modal-btn.cancel:hover { color: #bbb; }
.modal-btn.save {
	background: #f97316;
	color: #fff;
}
.modal-btn.save:hover { background: #ea6b0a; }

/* Add camera toggle button */
.add-camera-toggle {
	display: flex;
	align-items: center;
	gap: 6px;
	background: transparent;
	border: 1px dashed #333;
	border-radius: 6px;
	color: #888;
	padding: 8px 12px;
	cursor: pointer;
	font-size: 0.85rem;
	width: 100%;
	transition: color 0.15s, border-color 0.15s;
	margin-bottom: 10px;
}
.add-camera-toggle:hover { color: #f97316; border-color: #f97316; }
.add-camera-toggle-icon {
	font-size: 1.1rem;
	font-weight: 700;
	line-height: 1;
}

/* Collapsible add-camera form */
.add-camera-form {
	max-height: 0;
	overflow: hidden;
	transition: max-height 0.25s ease-out, opacity 0.2s;
	opacity: 0;
}
.add-camera-form.open {
	max-height: 400px;
	opacity: 1;
}

/* Advanced settings toggle */
.advanced-toggle {
	margin-bottom: 10px;
}
.advanced-toggle a {
	color: #555;
	font-size: 0.8rem;
	text-decoration: none;
}
.advanced-toggle a:hover { color: #888; }

.advanced-fields {
	max-height: 0;
	overflow: hidden;
	transition: max-height 0.2s ease-out;
}
.advanced-fields.open {
	max-height: 80px;
}

.settings-row {
	display: flex;
	align-items: center;
	gap: 10px;
	margin-bottom: 10px;
}

.settings-row label {
	width: 90px;
	font-size: 0.9rem;
	color: #aaa;
}

.settings-row input {
	flex: 1;
	background: #16213e;
	border: 1px solid #0f3460;
	color: #e0e0e0;
	padding: 8px 10px;
	border-radius: 6px;
	font-size: 0.9rem;
}

.connect-btn {
	width: 100%;
	padding: 10px;
	background: #f97316;
	color: #fff;
	border: none;
	border-radius: 8px;
	font-size: 1rem;
	font-weight: 700;
	cursor: pointer;
}
.connect-btn:active   { background: #ea6b0a; }
.connect-btn:disabled { background: #7a2030; cursor: not-allowed; }

/* ---- NDI preview ---- */
.preview-box {
	background: #000;
	border-radius: 8px;
	overflow: hidden;
	margin-bottom: 16px;
	aspect-ratio: 16 / 9;
	display: flex;
	align-items: center;
	justify-content: center;
	position: relative;
}
.preview-box img {
	width: 100%;
	height: 100%;
	object-fit: contain;
	display: none;
}
.preview-no-signal {
	color: #555;
	font-size: 0.85rem;
}

/* ---- Active camera indicator above D-pad ---- */
.active-camera {
	display: flex;
	align-items: center;
	justify-content: center;
	gap: 8px;
	padding: 10px 16px;
	border-radius: 8px;
	margin-bottom: 16px;
	font-size: 1rem;
	font-weight: 600;
	text-align: center;
}
.active-camera.connected {
	background: #0d2b0d;
	border: 1px solid #22c55e;
	color: #4ade80;
}
.active-camera.disconnected {
	background: #2a1a00;
	border: 1px solid #7a5020;
	color: #a07030;
}
.active-dot {
	width: 8px;
	height: 8px;
	border-radius: 50%;
	background: #4ade80;
	/* Pulsing animation draws attention to the live indicator */
	animation: pulse 2s ease-in-out infinite;
}
@keyframes pulse {
	0%, 100% { opacity: 1; }
	50% { opacity: 0.4; }
}

/* ---- Toast notifications ---- */
.toast-container {
	position: fixed;
	top: 16px;
	right: 16px;
	z-index: 1000;
	display: flex;
	flex-direction: column;
	gap: 8px;
	pointer-events: none;
}
.toast {
	padding: 10px 16px;
	border-radius: 8px;
	font-size: 0.85rem;
	font-weight: 500;
	pointer-events: auto;
	animation: toast-in 0.25s ease-out;
	max-width: 320px;
}
.toast.error {
	background: #3d1a00;
	color: #f97316;
	border: 1px solid #f97316;
}
.toast.success {
	background: #0f3d0f;
	color: #4caf50;
	border: 1px solid #4caf50;
}
@keyframes toast-in {
	from { opacity: 0; transform: translateX(20px); }
	to   { opacity: 1; transform: translateX(0); }
}
`
}

// jsScript returns the inline JavaScript that wires up button clicks,
// wheel-based zooming (with debounced stop), and settings/preset AJAX calls.
func jsScript() string {
	return `
// ---- Toast notifications ----
// Shows a brief, auto-dismissing message in the top-right corner.
// type: "error" (orange) or "success" (green).
function showToast(msg, type) {
	let container = document.getElementById('toast-container');
	let toast = document.createElement('div');
	toast.className = 'toast ' + (type || 'error');
	toast.textContent = msg;
	container.appendChild(toast);
	setTimeout(function() { toast.remove(); }, 3000);
}

// Shared fetch helper — posts form data, parses JSON, and surfaces errors via toast.
function postJSON(url, body) {
	return fetch(url, {
		method: 'POST',
		headers: {'Content-Type':'application/x-www-form-urlencoded'},
		body: body
	})
	.then(function(r) { return r.json(); })
	.then(function(data) {
		if (data.error) { showToast(data.error, 'error'); }
		return data;
	})
	.catch(function() { showToast('Request failed — network error', 'error'); });
}

// ---- Movement & speed curve state ----
// currentCurve: determines how speed changes over time while a D-pad button is held.
// maxSpeed: target speed set by the slider (1–24).
let currentCurve = 'constant';
let maxSpeed = 8;
let moveInterval = null;   // setInterval handle for ramping
let moveStartTime = 0;     // timestamp when button was pressed
let currentDirection = '';  // direction currently being held

// rampDurationMs: total time (ms) to ramp from 1 to maxSpeed for non-constant curves
const rampDurationMs = 2000;
// moveIntervalMs: how often (ms) we re-send the move command with updated speed
const moveIntervalMs = 100;

// setCurve switches the active speed curve and updates the button group styling.
function setCurve(curve) {
	currentCurve = curve;
	document.querySelectorAll('.curve-btn').forEach(function(btn) {
		btn.classList.remove('active');
	});
	document.getElementById('curve-' + curve).classList.add('active');
}

// updateSpeedDisplay updates the numeric readout next to the slider.
function updateSpeedDisplay(val) {
	maxSpeed = parseInt(val);
	document.getElementById('speed-value').textContent = val;
}

// computeSpeed calculates the current speed based on elapsed hold time and curve type.
// Returns an integer between 1 and maxSpeed.
//
//   constant:  always maxSpeed (no ramping)
//   linear:    linearly interpolates from 1 to maxSpeed over rampDurationMs
//   expo:      cubic easing (t^3) — slow start, dramatic acceleration
function computeSpeed(elapsedMs) {
	if (currentCurve === 'constant') {
		return maxSpeed;
	}
	// t is normalized progress [0.0, 1.0]
	let t = Math.min(1.0, elapsedMs / rampDurationMs);

	if (currentCurve === 'linear') {
		return Math.max(1, Math.round(1 + (maxSpeed - 1) * t));
	}

	// Expo: cubic easing — t^3 gives a slow-start, fast-finish feel
	let curved = t * t * t;
	return Math.max(1, Math.round(1 + (maxSpeed - 1) * curved));
}

// startMove begins a D-pad hold: sends the initial move command, then for
// non-constant curves starts a periodic interval that re-sends with increasing speed.
function startMove(dir) {
	if (moveInterval) stopMove();

	currentDirection = dir;
	moveStartTime = Date.now();

	// Send the first command immediately at the curve's initial speed
	sendMoveWithSpeed(dir, computeSpeed(0));

	// For constant curve, one command is enough — VISCA keeps moving until stop
	if (currentCurve === 'constant') return;

	// For ramping curves, periodically re-send with increasing speed
	moveInterval = setInterval(function() {
		let elapsed = Date.now() - moveStartTime;
		let speed = computeSpeed(elapsed);
		sendMoveWithSpeed(currentDirection, speed);

		// Once we've reached max speed, stop re-sending (camera holds the speed)
		if (elapsed >= rampDurationMs) {
			clearInterval(moveInterval);
			moveInterval = null;
		}
	}, moveIntervalMs);
}

// stopMove clears the ramp interval and sends a stop command to the camera.
function stopMove() {
	if (moveInterval) {
		clearInterval(moveInterval);
		moveInterval = null;
	}
	currentDirection = '';
	sendMove('stop');
}

// sendMoveWithSpeed sends a move command with explicit speed parameters.
// Pan speed clamped to 1–24 (VISCA max 0x18), tilt speed to 1–23 (0x17).
function sendMoveWithSpeed(dir, speed) {
	let panSpeed = Math.min(24, Math.max(1, speed));
	let tiltSpeed = Math.min(23, Math.max(1, speed));
	postJSON('/api/move', 'direction=' + dir +
		'&panSpeed=' + panSpeed + '&tiltSpeed=' + tiltSpeed);
}

// sendMove sends a direction-only command (used for stop and home).
function sendMove(dir) {
	postJSON('/api/move', 'direction=' + dir);
}

// ---- Zoom via scroll wheel ----
// Forward scroll (deltaY < 0) = zoom in, backward (deltaY > 0) = zoom out.
// A debounce timer sends zoom-stop after scrolling ends.
let zoomTimer = null;
let zoomLevel = 50; // visual indicator percentage

document.addEventListener('wheel', function(e) {
	e.preventDefault();

	// Map scroll intensity to zoom speed (1-7)
	let speed = Math.min(7, Math.max(1, Math.ceil(Math.abs(e.deltaY) / 30)));
	let action = e.deltaY < 0 ? 'in' : 'out';

	// Update visual indicator
	let delta = e.deltaY < 0 ? 3 : -3;
	zoomLevel = Math.min(100, Math.max(0, zoomLevel + delta));
	document.getElementById('zoom-indicator').style.width = zoomLevel + '%';

	postJSON('/api/zoom', 'action=' + action + '&speed=' + speed);

	// Debounce: stop zooming 200ms after last scroll event
	clearTimeout(zoomTimer);
	zoomTimer = setTimeout(function() {
		postJSON('/api/zoom', 'action=stop');
	}, 200);
}, {passive: false});

// ---- Presets ----
function presetRecall(num) {
	postJSON('/api/preset/recall', 'num=' + num);
}

function presetSet(num) {
	// Include the preset's label in the confirmation so the operator
	// knows exactly which slot they're overwriting.
	let labelEl = document.getElementById('preset-label-' + num);
	let name = (labelEl && labelEl.value) ? '"' + labelEl.value + '"' : 'Preset ' + (num + 1);
	if (!confirm('Save current camera position to ' + name + '?')) return;
	postJSON('/api/preset/set', 'num=' + num);
}

function saveLabel(num, label) {
	postJSON('/api/preset/label', 'num=' + num + '&label=' + encodeURIComponent(label));
}

// ---- Camera management ----

// Toggle the add-camera form open/closed.
function toggleAddForm() {
	let form = document.getElementById('add-camera-form');
	form.classList.toggle('open');
}

// Toggle advanced settings (port field) visibility.
function toggleAdvanced(event) {
	event.preventDefault();
	let fields = document.getElementById('advanced-fields');
	fields.classList.toggle('open');
}

// Connect to a saved camera — one click, no form needed.
function connectCamera(label, ip, port) {
	// Highlight the clicked card immediately for responsiveness
	document.querySelectorAll('.camera-item').forEach(function(el) {
		el.classList.remove('active');
	});

	doConnect(label, ip, port);
}

// Add a new camera from the form and connect to it.
function addCamera() {
	let label = document.getElementById('camera-label').value.trim();
	let ip    = document.getElementById('camera-ip').value.trim();
	let port  = document.getElementById('camera-port').value;

	if (!label) {
		showToast('Please enter a name for this camera', 'error');
		document.getElementById('camera-label').focus();
		return;
	}
	if (!ip) {
		showToast('Please enter an IP address', 'error');
		document.getElementById('camera-ip').focus();
		return;
	}

	doConnect(label, ip, port);
}

// Reconnect a saved camera (stop propagation so the card click doesn't fire).
function reconnectCamera(label, ip, port, event) {
	event.stopPropagation();
	showToast('Reconnecting to ' + label + '...', 'success');
	doConnect(label, ip, port);
}

// Open the edit modal for a saved camera.
function editCamera(label, ip, port, event) {
	event.stopPropagation();
	document.getElementById('edit-old-label').value = label;
	document.getElementById('edit-label').value = label;
	document.getElementById('edit-ip').value = ip;
	document.getElementById('edit-port').value = port;
	document.getElementById('edit-modal').classList.add('open');
}

// Close the edit modal. If called from overlay click, only close if the overlay itself was clicked.
function closeEditModal(event) {
	if (event && event.target !== event.currentTarget) return;
	document.getElementById('edit-modal').classList.remove('open');
}

// Save edited camera details.
function saveEditCamera() {
	let oldLabel = document.getElementById('edit-old-label').value;
	let label = document.getElementById('edit-label').value.trim();
	let ip = document.getElementById('edit-ip').value.trim();
	let port = document.getElementById('edit-port').value;

	if (!label) { showToast('Name is required', 'error'); return; }
	if (!ip) { showToast('IP address is required', 'error'); return; }

	postJSON('/api/camera/edit',
		'old_label=' + encodeURIComponent(oldLabel) +
		'&label=' + encodeURIComponent(label) +
		'&ip=' + encodeURIComponent(ip) +
		'&port=' + port
	).then(function(data) {
		if (data && !data.error) {
			closeEditModal();
			updateCameraList(data.cameras, data.ip, data.port, data.connected);
			if (data.connected) {
				updateStatus(true, data.label, data.ip, data.port);
			}
			showToast('Camera updated', 'success');
		}
	});
}

// Remove a saved camera from the list (stop propagation so the card click doesn't fire).
function removeCamera(label, event) {
	event.stopPropagation();
	if (!confirm('Delete camera "' + label + '"?')) return;
	postJSON('/api/camera/remove', 'label=' + encodeURIComponent(label))
	.then(function(data) {
		if (data) {
			updateCameraList(data.cameras, data.ip, data.port, data.connected);
		}
	});
}

// Shared connection logic used by both connectCamera and addCamera.
function doConnect(label, ip, port) {
	let btn = document.getElementById('connect-btn');
	if (btn) {
		btn.textContent = 'Connecting...';
		btn.disabled = true;
	}

	fetch('/api/settings', {
		method: 'POST',
		headers: {'Content-Type':'application/x-www-form-urlencoded'},
		body: 'label=' + encodeURIComponent(label) +
		      '&ip='   + encodeURIComponent(ip) +
		      '&port=' + port
	})
	.then(function(r) { return r.json(); })
	.then(function(data) {
		if (btn) {
			btn.disabled = false;
			btn.textContent = 'Add & Connect';
		}
		if (data.connected) {
			updateStatus(true, data.label, data.ip, data.port);
			updateCameraList(data.cameras, data.ip, data.port, true);
			showToast('Connected to ' + (data.label || data.ip), 'success');
			// Clear the add form and collapse it after successful add
			if (document.getElementById('camera-label')) {
				document.getElementById('camera-label').value = '';
				document.getElementById('camera-ip').value = '';
				document.getElementById('camera-port').value = '52381';
			}
			let form = document.getElementById('add-camera-form');
			if (form && data.cameras && data.cameras.length > 0) {
				form.classList.remove('open');
			}
		} else {
			updateStatus(false);
			updateCameraList(data.cameras, data.ip, data.port, false);
			showToast('Connection failed: ' + (data.error || 'unknown'), 'error');
		}
	})
	.catch(function() {
		if (btn) {
			btn.textContent = 'Add & Connect';
			btn.disabled = false;
		}
		showToast('Request failed — network error', 'error');
	});
}

// ---- Dynamic UI updates ----

function updateStatus(connected, label, ip, port) {
	let el = document.getElementById('conn-status');
	if (connected) {
		let displayName = label || ip;
		el.className = 'status connected';
		el.textContent = 'Connected \u2014 ' + displayName + ' (' + ip + ':' + port + ')';
	} else {
		el.className = 'status disconnected';
		el.textContent = 'Disconnected';
	}

	let active = document.getElementById('active-camera');
	if (connected) {
		active.className = 'active-camera connected';
		active.innerHTML = '<span class="active-dot"></span><span class="active-label">' +
			(label || ip) + '</span>';
	} else {
		active.className = 'active-camera disconnected';
		active.innerHTML = '<span class="active-label">No camera connected</span>';
	}
}

// ---- NDI preview via WebSocket ----
function startPreview() {
	let img = document.getElementById('preview-img');
	let noSignal = document.getElementById('preview-no-signal');
	if (!img) return;

	let proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
	let ws = new WebSocket(proto + '//' + location.host + '/api/preview');
	ws.binaryType = 'blob';

	ws.onmessage = function(e) {
		let url = URL.createObjectURL(e.data);
		img.onload = function() { URL.revokeObjectURL(url); };
		img.src = url;
		img.style.display = 'block';
		noSignal.style.display = 'none';
	};

	ws.onclose = function() {
		img.style.display = 'none';
		noSignal.style.display = 'block';
		setTimeout(startPreview, 3000);
	};

	ws.onerror = function() { ws.close(); };
}
startPreview();

// Rebuilds the saved cameras list and shows/hides the "no cameras" hint and toggle button.
function updateCameraList(cameras, activeIP, activePort, isConnected) {
	let section = document.querySelector('.cameras-section');
	if (!section) return;

	// Update or create saved-cameras container
	let container = section.querySelector('.saved-cameras');
	let hint = section.querySelector('.no-cameras-hint');
	let toggle = section.querySelector('.add-camera-toggle');

	if (!cameras || cameras.length === 0) {
		if (container) container.remove();
		// Show hint if not present
		if (!hint) {
			hint = document.createElement('p');
			hint.className = 'no-cameras-hint';
			hint.textContent = 'No cameras saved yet. Add one to get started.';
			let h2 = section.querySelector('h2');
			h2.insertAdjacentElement('afterend', hint);
		}
		// Remove toggle button when no cameras
		if (toggle) toggle.remove();
		// Open the add form
		let form = document.getElementById('add-camera-form');
		if (form) form.classList.add('open');
		return;
	}

	// Remove hint if cameras exist
	if (hint) hint.remove();

	// Create container if needed
	if (!container) {
		container = document.createElement('div');
		container.className = 'saved-cameras';
		let h2 = section.querySelector('h2');
		h2.insertAdjacentElement('afterend', container);
	}

	// Ensure toggle button exists
	if (!toggle) {
		toggle = document.createElement('button');
		toggle.className = 'add-camera-toggle';
		toggle.onclick = toggleAddForm;
		toggle.innerHTML = '<span class="add-camera-toggle-icon">+</span><span>Add Camera</span>';
		let form = document.getElementById('add-camera-form');
		section.insertBefore(toggle, form);
	}

	let html = '';
	for (let cam of cameras) {
		let isActive = isConnected && cam.ip === activeIP && cam.port === activePort;
		let cls = isActive ? 'camera-item active' : 'camera-item';
		let escapedLabel = cam.label.replace(/"/g, '&quot;');
		let esc = escapedLabel.replace(/'/g, "\\'");
		html += '<div class="' + cls + '" onclick="connectCamera(\'' +
			esc + "','" + cam.ip + "','" + cam.port + "')\">" +
			'<div class="camera-item-info">' +
			'<span class="camera-name">' + cam.label + '</span>' +
			'<span class="camera-addr">' + cam.ip + ':' + cam.port + '</span>' +
			'</div>' +
			'<div class="camera-actions">' +
			'<button class="camera-action-btn reconnect" onclick="reconnectCamera(\'' +
			esc + "','" + cam.ip + "','" + cam.port + "',event)\" title=\"Reconnect\">\u21bb</button>" +
			'<button class="camera-action-btn edit" onclick="editCamera(\'' +
			esc + "','" + cam.ip + "','" + cam.port + "',event)\" title=\"Edit\">\u270e</button>" +
			'<button class="camera-action-btn delete" onclick="removeCamera(\'' +
			esc + "',event)\" title=\"Delete\">\u00d7</button>" +
			'</div>' +
			'</div>';
	}
	container.innerHTML = html;
}
`
}
