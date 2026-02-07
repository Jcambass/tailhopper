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

function connectSelected(machine) {
  var otherInput = document.querySelector('[data-other-port="' + machine + '"]');
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

  // Get the DNS name from the machine element's data-dnsname attribute
  var machineEl = document.querySelector('[data-machine="' + machine + '"]');
  var dnsName = machineEl ? machineEl.getAttribute('data-dnsname') : machine;
  
  // Remove trailing dot if present
  if (dnsName.endsWith('.')) {
    dnsName = dnsName.slice(0, -1);
  }

  var httpsToggle = document.querySelector('[data-https="' + machine + '"]');
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
  var connectBtn = document.querySelector('[data-connect="' + machine + '"]');
  if (!connectBtn) {
    return;
  }

  var selected = document.querySelector('input[name="port-choose-' + machine + '"]:checked');
  var otherInput = document.querySelector('[data-other-port="' + machine + '"]');
  var otherPort = otherInput ? parseInt(otherInput.value, 10) : NaN;
  var httpsToggle = document.querySelector('[data-https="' + machine + '"]');

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
