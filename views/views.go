package views

import (
	"camctl/presets"
	"fmt"

	"github.com/rohanthewiz/element"
)

// Settings holds the current camera connection state for rendering.
type Settings struct {
	CameraIP   string
	CameraPort int
	Connected  bool
}

// PageData bundles everything the main page template needs.
type PageData struct {
	Settings Settings
	Presets  []presets.Preset
}

// RenderPage produces the full HTML page: D-pad, zoom indicator, presets, and settings.
func RenderPage(data PageData) string {
	b := element.NewBuilder()

	b.WriteString("<!DOCTYPE html>")
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
						renderDPad(b),

						// Zoom indicator
						b.DivClass("section").R(
							b.H2().T("Zoom"),
							b.PClass("zoom-hint").T("Scroll wheel to zoom in / out"),
							b.DivClass("zoom-bar").R(
								b.Div("id", "zoom-indicator", "class", "zoom-level").R(),
							),
						),
					),

					// Right column — presets and camera settings
					b.DivClass("col").R(
						renderPresets(b, data.Presets),
						renderSettings(b, data.Settings),
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
		statusText = fmt.Sprintf("Connected to %s:%d", s.CameraIP, s.CameraPort)
	}
	b.Div("id", "conn-status", "class", statusClass).T(statusText)
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

// dpadButton creates a single D-pad button that sends a move command on press
// and a stop command on release. Home is a one-shot command (no stop needed).
func dpadButton(b *element.Builder, direction, label, id string) *element.Builder {
	if direction == "home" {
		b.Button("id", id, "class", "dpad-cell dpad-btn",
			"onmousedown", fmt.Sprintf("sendMove('%s')", direction),
			"ontouchstart", fmt.Sprintf("sendMove('%s'); event.preventDefault()", direction),
		).T(label)
	} else {
		b.Button("id", id, "class", "dpad-cell dpad-btn",
			"onmousedown", fmt.Sprintf("sendMove('%s')", direction),
			"onmouseup", "sendMove('stop')",
			"onmouseleave", "sendMove('stop')",
			"ontouchstart", fmt.Sprintf("sendMove('%s'); event.preventDefault()", direction),
			"ontouchend", "sendMove('stop')",
		).T(label)
	}
	return b
}

// renderPresets creates 6 presets in a 3-per-row grid.
// Each preset card has an editable label, a Recall button, and a Save button.
func renderPresets(b *element.Builder, prs []presets.Preset) *element.Builder {
	b.DivClass("section").R(
		b.H2().T("Presets"),
		b.DivClass("preset-grid").R(
			b.Wrap(func() {
				for _, p := range prs {
					b.DivClass("preset-card").R(
						b.Input("type", "text", "class", "preset-label",
							"id", fmt.Sprintf("preset-label-%d", p.Number),
							"value", p.Label,
							"onchange", fmt.Sprintf("saveLabel(%d, this.value)", p.Number),
						).R(),
						b.DivClass("preset-actions").R(
							b.Button("class", "preset-btn recall",
								"onclick", fmt.Sprintf("presetRecall(%d)", p.Number),
							).T("Recall"),
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

// renderSettings creates the camera connection form.
func renderSettings(b *element.Builder, s Settings) *element.Builder {
	portStr := fmt.Sprintf("%d", s.CameraPort)
	if s.CameraPort == 0 {
		portStr = "52381"
	}

	b.DivClass("section settings").R(
		b.H2().T("Camera Settings"),
		b.DivClass("settings-row").R(
			b.Label("for", "camera-ip").T("IP Address"),
			b.Input("type", "text", "id", "camera-ip",
				"value", s.CameraIP, "placeholder", "192.168.1.100").R(),
		),
		b.DivClass("settings-row").R(
			b.Label("for", "camera-port").T("Port"),
			b.Input("type", "number", "id", "camera-port",
				"value", portStr, "placeholder", "52381").R(),
		),
		b.DivClass("settings-row").R(
			b.Button("id", "connect-btn", "class", "connect-btn",
				"onclick", "saveSettings()",
			).T("Connect"),
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
	color: #e94560;
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
.disconnected { background: #3d0f0f; color: #f44336; border: 1px solid #f44336; }

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
	background: #e94560;
	color: #fff;
	transform: scale(0.95);
}

#btn-home {
	background: #0f3460;
	border-color: #e94560;
}

/* ---- Zoom ---- */
.zoom-hint {
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
	background: #e94560;
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
	color: #e0e0e0;
	padding: 6px 8px;
	border-radius: 4px;
	font-size: 0.85rem;
	text-align: center;
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
	font-weight: 600;
	font-size: 0.8rem;
}

.preset-btn.recall {
	background: #0f3460;
	color: #e0e0e0;
}
.preset-btn.recall:active { background: #e94560; color: #fff; }

.preset-btn.save {
	background: #533483;
	color: #e0e0e0;
}
.preset-btn.save:active { background: #e94560; color: #fff; }

/* ---- Settings ---- */
.settings {
	border-top: 1px solid #333;
	padding-top: 20px;
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
	background: #e94560;
	color: #fff;
	border: none;
	border-radius: 8px;
	font-size: 1rem;
	font-weight: 700;
	cursor: pointer;
}
.connect-btn:active { background: #c73650; }
`
}

// jsScript returns the inline JavaScript that wires up button clicks,
// wheel-based zooming (with debounced stop), and settings/preset AJAX calls.
func jsScript() string {
	return `
// ---- Movement ----
function sendMove(dir) {
	fetch('/api/move', {
		method: 'POST',
		headers: {'Content-Type':'application/x-www-form-urlencoded'},
		body: 'direction=' + dir
	});
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

	fetch('/api/zoom', {
		method: 'POST',
		headers: {'Content-Type':'application/x-www-form-urlencoded'},
		body: 'action=' + action + '&speed=' + speed
	});

	// Debounce: stop zooming 200ms after last scroll event
	clearTimeout(zoomTimer);
	zoomTimer = setTimeout(function() {
		fetch('/api/zoom', {
			method: 'POST',
			headers: {'Content-Type':'application/x-www-form-urlencoded'},
			body: 'action=stop'
		});
	}, 200);
}, {passive: false});

// ---- Presets ----
function presetRecall(num) {
	fetch('/api/preset/recall', {
		method: 'POST',
		headers: {'Content-Type':'application/x-www-form-urlencoded'},
		body: 'num=' + num
	});
}

function presetSet(num) {
	if (!confirm('Save current position to this preset?')) return;
	fetch('/api/preset/set', {
		method: 'POST',
		headers: {'Content-Type':'application/x-www-form-urlencoded'},
		body: 'num=' + num
	});
}

function saveLabel(num, label) {
	fetch('/api/preset/label', {
		method: 'POST',
		headers: {'Content-Type':'application/x-www-form-urlencoded'},
		body: 'num=' + num + '&label=' + encodeURIComponent(label)
	});
}

// ---- Settings ----
function saveSettings() {
	let ip = document.getElementById('camera-ip').value;
	let port = document.getElementById('camera-port').value;
	let btn = document.getElementById('connect-btn');
	btn.textContent = 'Connecting...';

	fetch('/api/settings', {
		method: 'POST',
		headers: {'Content-Type':'application/x-www-form-urlencoded'},
		body: 'ip=' + encodeURIComponent(ip) + '&port=' + port
	})
	.then(r => r.json())
	.then(data => {
		let el = document.getElementById('conn-status');
		if (data.connected) {
			el.className = 'status connected';
			el.textContent = 'Connected to ' + ip + ':' + port;
			btn.textContent = 'Connected';
		} else {
			el.className = 'status disconnected';
			el.textContent = 'Connection failed: ' + (data.error || 'unknown');
			btn.textContent = 'Connect';
		}
	})
	.catch(() => {
		btn.textContent = 'Connect';
	});
}
`
}
