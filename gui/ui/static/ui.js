function copyToClipboard(elementId) {
  var el = document.getElementById(elementId);
  if (!el) return;
  
  var text = el.textContent || el.innerText;
  navigator.clipboard.writeText(text).then(function() {
    // Show brief feedback
    var btn = el.nextElementSibling;
    if (btn && btn.classList.contains('copy-btn')) {
      var originalText = btn.textContent;
      btn.textContent = 'Copied!';
      setTimeout(function() {
        btn.textContent = originalText;
      }, 1500);
    }
  }).catch(function(err) {
    console.error('Copy failed:', err);
  });
}

function refreshConnections() {
  fetch('/api/connections')
    .then(function(r) { return r.json(); })
    .then(function(connections) {
      var container = document.getElementById('connections-list');
      if (!container) return;
      
      if (!connections || connections.length === 0) {
        container.innerHTML = '<p class="no-connections">No connections yet. Configure your browser with the PAC file above to get started.</p>';
        return;
      }
      
      var html = '<table class="connections-table"><thead><tr>';
      html += '<th>Status</th><th>Target</th><th>Duration</th><th>Data</th>';
      html += '</tr></thead><tbody>';
      
      connections.forEach(function(conn) {
        var statusClass = conn.Active ? 'active' : (conn.Error ? 'error' : 'done');
        var statusDot = '<span class="status-dot ' + statusClass + '"></span>';
        var duration = conn.Active ? 'ongoing' : formatDurationJS(conn.StartTime, conn.EndTime);
        var dataStr = '↑' + formatBytesJS(conn.BytesSent) + ' ↓' + formatBytesJS(conn.BytesRecv);
        
        html += '<tr class="' + (conn.Active ? 'active' : (conn.Error ? 'error' : '')) + '">';
        html += '<td>' + statusDot + '</td>';
        html += '<td class="target">' + conn.Host + ':' + conn.Port + '</td>';
        html += '<td class="duration">' + duration + '</td>';
        html += '<td class="data">' + dataStr + '</td>';
        html += '</tr>';
      });
      
      html += '</tbody></table>';
      container.innerHTML = html;
    })
    .catch(function(err) {
      console.error('Failed to refresh connections:', err);
    });
}

function formatDurationJS(startStr, endStr) {
  if (!endStr) return 'ongoing';
  var start = new Date(startStr);
  var end = new Date(endStr);
  var ms = end - start;
  if (ms < 1000) return ms + 'ms';
  if (ms < 60000) return (ms / 1000).toFixed(1) + 's';
  return (ms / 60000).toFixed(1) + 'm';
}

function formatBytesJS(bytes) {
  if (bytes < 1024) return bytes + 'B';
  if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + 'KB';
  if (bytes < 1024 * 1024 * 1024) return (bytes / (1024 * 1024)).toFixed(1) + 'MB';
  return (bytes / (1024 * 1024 * 1024)).toFixed(1) + 'GB';
}

// Auto-refresh connections every 5 seconds
setInterval(refreshConnections, 5000);

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

  // machine is now the DNS name from data-machine attribute
  var dnsName = machine;
  
  // Remove trailing dot if present
  if (dnsName.endsWith('.')) {
    dnsName = dnsName.slice(0, -1);
  }

  var httpsToggle = document.getElementById('https-' + machine);
  var scheme = httpsToggle && httpsToggle.checked ? 'https' : 'http';
  
  // Open URL directly - browser will use PAC file to route through SOCKS5
  var url = scheme + '://' + dnsName + ':' + port + '/';
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

// Track which machines are being interacted with
var activeMachines = {};

function markMachineActive(machine) {
  activeMachines[machine] = Date.now();
}

function isMachineActive(machine) {
  var lastActive = activeMachines[machine];
  if (!lastActive) return false;
  // Consider active for 10 seconds after last interaction
  return (Date.now() - lastActive) < 10000;
}

// Add event listeners to track user interaction with machines
document.addEventListener('click', function(e) {
  var machineEl = e.target.closest('.machine');
  if (machineEl) {
    var machine = machineEl.getAttribute('data-machine');
    if (machine) markMachineActive(machine);
  }
});

document.addEventListener('focus', function(e) {
  var machineEl = e.target.closest('.machine');
  if (machineEl) {
    var machine = machineEl.getAttribute('data-machine');
    if (machine) markMachineActive(machine);
  }
}, true);

function refreshMachines() {
  fetch('/api/machines')
    .then(function(r) { return r.json(); })
    .then(function(machines) {
      var existingMachines = {};
      document.querySelectorAll('.machine').forEach(function(el) {
        var dnsName = el.getAttribute('data-machine');
        if (dnsName) existingMachines[dnsName] = el;
      });

      var newMachineDNSNames = {};
      machines.forEach(function(m) {
        newMachineDNSNames[m.DNSName] = true;

        if (existingMachines[m.DNSName]) {
          // Update existing machine - only update status if not being interacted with
          if (!isMachineActive(m.DNSName)) {
            var el = existingMachines[m.DNSName];
            // Update online/offline status
            var statusSpan = el.querySelector('.machine-name span');
            if (statusSpan) {
              statusSpan.className = m.StatusClass;
              statusSpan.textContent = '(' + m.StatusText + ')';
            }
            // Update IPs
            var ipsEl = el.querySelector('.machine-addrs');
            if (ipsEl) {
              ipsEl.textContent = 'IPs: ' + m.IPs;
            }
          }
        } else {
          // New machine - add it to the list
          addNewMachine(m);
        }
      });

      // Remove machines that no longer exist
      Object.keys(existingMachines).forEach(function(dnsName) {
        if (!newMachineDNSNames[dnsName] && !isMachineActive(dnsName)) {
          existingMachines[dnsName].remove();
        }
      });

      // Update empty state
      updateEmptyState(machines.length);
    })
    .catch(function(err) {
      console.error('Failed to refresh machines:', err);
    });
}

function updateEmptyState(machineCount) {
  var emptyState = document.querySelector('.empty-state');
  var machinesTitle = document.querySelector('.machines-title');
  
  if (machineCount === 0) {
    if (!emptyState && machinesTitle) {
      var div = document.createElement('div');
      div.className = 'empty-state';
      div.innerHTML = '<p class="empty-state-message">No machines found on your tailnet.</p>' +
        '<p class="empty-state-hint">Make sure other devices are connected to your Tailscale network and that your ACLs allow this device to see them. If you modify your ACLs, refresh this page to see the updated list.</p>';
      machinesTitle.insertAdjacentElement('afterend', div);
    }
  } else {
    if (emptyState) {
      emptyState.remove();
    }
  }
}

function addNewMachine(m) {
  var machinesTitle = document.querySelector('.machines-title');
  if (!machinesTitle) return;

  var html = '<div class="machine" data-machine="' + escapeHtml(m.DNSName) + '">' +
    '<div class="machine-header">' +
    '<div>' +
    '<div class="machine-name">' + escapeHtml(m.Name) + ' <span class="' + m.StatusClass + '">(' + m.StatusText + ')</span></div>' +
    '<div class="machine-dns">' + escapeHtml(m.DNSName) + '</div>' +
    '</div>' +
    '<button class="scan-button" onclick="scanMachine(\'' + escapeHtml(m.DNSName) + '\', this)">Scan For Ports</button>' +
    '</div>' +
    '<div id="ports-' + escapeHtml(m.DNSName) + '" class="ports-container">' +
    '<span class="ports-empty">No ports scanned yet</span>' +
    '</div>' +
    '<div class="controls-row hidden" id="controls-' + escapeHtml(m.DNSName) + '">' +
    '<label class="scheme-toggle">' +
    '<span class="scheme-label">HTTP</span>' +
    '<input type="checkbox" id="https-' + escapeHtml(m.DNSName) + '">' +
    '<span class="scheme-slider"></span>' +
    '<span class="scheme-label">HTTPS</span>' +
    '</label>' +
    '<button id="connect-' + escapeHtml(m.DNSName) + '" onclick="connectSelected(\'' + escapeHtml(m.DNSName) + '\')" disabled>' +
    'Open <span class="connect-icon" aria-hidden="true"><svg viewBox="0 0 24 24" class="connect-icon-svg" focusable="false" aria-hidden="true"><path d="M14 3h7v7h-2V6.41l-9.29 9.3-1.42-1.42 9.3-9.29H14V3z"></path><path d="M5 5h6v2H7v10h10v-4h2v6H5V5z"></path></svg></span>' +
    '</button>' +
    '</div>' +
    '<div class="machine-addrs">IPs: ' + escapeHtml(m.IPs) + '</div>' +
    '</div>';

  // Find the right position (alphabetically sorted)
  var inserted = false;
  var allMachines = document.querySelectorAll('.machine');
  for (var i = 0; i < allMachines.length; i++) {
    var existingName = allMachines[i].getAttribute('data-machine');
    if (existingName && m.DNSName.toLowerCase() < existingName.toLowerCase()) {
      allMachines[i].insertAdjacentHTML('beforebegin', html);
      inserted = true;
      break;
    }
  }
  if (!inserted) {
    // Add at the end, before empty state if present
    var emptyState = document.querySelector('.empty-state');
    if (emptyState) {
      emptyState.insertAdjacentHTML('beforebegin', html);
    } else {
      // Find the last machine or add after title
      var lastMachine = document.querySelector('.machine:last-of-type');
      if (lastMachine) {
        lastMachine.insertAdjacentHTML('afterend', html);
      } else {
        machinesTitle.insertAdjacentHTML('afterend', html);
      }
    }
  }
}

function escapeHtml(text) {
  var div = document.createElement('div');
  div.textContent = text;
  return div.innerHTML;
}

// Auto-refresh machines every 10 seconds
setInterval(refreshMachines, 10000);

