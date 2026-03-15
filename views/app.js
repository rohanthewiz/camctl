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

// ---- Zoom settings ----

// Toggle the advanced zoom controls panel open/closed.
function toggleZoomSettings() {
	let panel = document.getElementById('zoom-settings-panel');
	panel.classList.toggle('open');
}

// ---- Preview settings ----

// Toggle the preview settings panel open/closed.
function togglePreviewSettings() {
	let panel = document.getElementById('preview-settings-panel');
	panel.classList.toggle('open');
}

// Save preview protocol preferences and restart the preview.
function savePreviewSettings() {
	let body =
		'enable_ndi='  + document.getElementById('pv-ndi').checked +
		'&enable_obs=' + document.getElementById('pv-obs').checked +
		'&enable_http=' + document.getElementById('pv-http').checked +
		'&enable_rtsp=' + document.getElementById('pv-rtsp').checked +
		'&obs_ws_host=' + encodeURIComponent(document.getElementById('pv-obs-host').value) +
		'&obs_ws_password=' + encodeURIComponent(document.getElementById('pv-obs-password').value);

	postJSON('/api/preview/settings', body).then(function(data) {
		if (data && data.status === 'ok') {
			showToast('Preview settings saved', 'success');
		}
	});
}

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