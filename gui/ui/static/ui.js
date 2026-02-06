function renderPorts(machine, ports) {
  var container = document.getElementById('ports-' + machine);
  if (!container) {
    return;
  }

  var html = '';
  html += '<div class="ports-list">';
  if (ports && ports.length > 0) {
    ports.forEach(function (p, i) {
      var checked = i === 0 ? ' checked' : '';
      html += '<label class="port-option"><input type="radio" name="port-choose-' + machine + '" value="' + p + '"' + checked + ' onchange="updateConnectState(\'' + machine + '\')">' + p + '</label>';
    });
  } else {
    html += '<span class="ports-empty">No open ports found</span>';
  }
  var otherChecked = !ports || ports.length === 0 ? ' checked' : '';
  html += '<label class="port-option">';
  html += '<input type="radio" name="port-choose-' + machine + '" value="other"' + otherChecked + ' onchange="updateConnectState(\'' + machine + '\')">Other';
  html += '<input type="number" id="other-port-' + machine + '" min="1" max="65535" placeholder="e.g., 3000" onfocus="selectOther(\'' + machine + '\')" oninput="updateConnectState(\'' + machine + '\')" onkeypress="handleKeyPress(event, \'' + machine + '\')">';
  html += '</label>';
  html += '</div>';

  container.innerHTML = html;

  var controls = document.getElementById('controls-' + machine);
  if (controls) {
    controls.classList.remove('hidden');
  }

  var httpsToggle = document.getElementById('https-' + machine);
  if (httpsToggle) {
    var firstPort = ports && ports.length > 0 ? parseInt(ports[0], 10) : NaN;
    httpsToggle.checked = firstPort === 443 || firstPort === 8448;
  }

  updateConnectState(machine);
}

function scanMachine(machine, button) {
  var btn = button || (typeof event !== 'undefined' ? event.target : null);
  if (!btn) {
    return;
  }

  btn.disabled = true;
  btn.textContent = 'Scanning...';

  fetch('/api/scan', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ machine: machine })
  })
    .then(function (r) { return r.json(); })
    .then(function (data) { renderPorts(machine, data.ports); })
    .catch(function (err) {
      var container = document.getElementById('ports-' + machine);
      if (container) {
        container.innerHTML = '<span class="ports-empty" style="color: red;">Scan failed</span>';
      }
      console.error(err);
    })
    .finally(function () {
      btn.disabled = false;
      btn.textContent = 'Scan';
    });
}

function connectSelected(machine) {
  var otherInput = document.getElementById('other-port-' + machine);
  var otherPort = otherInput ? parseInt(otherInput.value, 10) : NaN;
  var selected = document.querySelector('input[name="port-choose-' + machine + '"]:checked');

  var port = NaN;
  if (selected && selected.value === 'other') {
    if (!isNaN(otherPort) && otherPort >= 1 && otherPort <= 65535) {
      port = otherPort;
    }
  } else if (selected) {
    port = parseInt(selected.value, 10);
  } else if (!isNaN(otherPort) && otherPort >= 1 && otherPort <= 65535) {
    port = otherPort;
  }

  if (isNaN(port) || port < 1 || port > 65535) {
    alert('Please select or enter a valid port number (1-65535)');
    return;
  }

  var httpsToggle = document.getElementById('https-' + machine);
  var prefix = httpsToggle && httpsToggle.checked ? '/proxy/https' : '/proxy';
  var url = prefix + '/' + machine + '/' + port + '/';
  window.open(url, '_blank', 'noopener');
}

function handleKeyPress(event, machine) {
  if (event.key === 'Enter') {
    connectSelected(machine);
  }
}

function selectOther(machine) {
  var otherRadio = document.querySelector('input[name="port-choose-' + machine + '"][value="other"]');
  if (otherRadio) {
    otherRadio.checked = true;
  }
  updateConnectState(machine);
}

function updateConnectState(machine) {
  var connectBtn = document.getElementById('connect-' + machine);
  if (!connectBtn) {
    return;
  }

  var selected = document.querySelector('input[name="port-choose-' + machine + '"]:checked');
  var otherInput = document.getElementById('other-port-' + machine);
  var otherPort = otherInput ? parseInt(otherInput.value, 10) : NaN;
  var httpsToggle = document.getElementById('https-' + machine);

  if (selected && selected.value === 'other') {
    connectBtn.disabled = isNaN(otherPort) || otherPort < 1 || otherPort > 65535;
  } else {
    connectBtn.disabled = false;
  }

  if (httpsToggle && selected && selected.value !== 'other') {
    var portValue = parseInt(selected.value, 10);
    httpsToggle.checked = portValue === 443 || portValue === 8448;
  }
}

