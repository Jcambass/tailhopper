function copyToClipboard(elementId) {
  var el = document.getElementById(elementId);
  if (!el) return;
  
  var text = el.textContent || el.innerText;
  navigator.clipboard.writeText(text).then(function() {
    // Show brief feedback by swapping icons
    var container = el.closest('.code-block') || el.closest('.tailnet-socks-value');
    var btn = container ? container.querySelector('.copy-btn') : null;
    if (btn) {
      btn.classList.add('copied');
      setTimeout(function() {
        btn.classList.remove('copied');
      }, 1500);
    }
  }).catch(function(err) {
    console.error('Copy failed:', err);
  });
}

function isToggleRequest(evt) {
  if (!evt || !evt.detail) return false;
  var elt = evt.detail.elt;
  if (elt) {
    var post = elt.getAttribute('hx-post') || elt.getAttribute('data-hx-post');
    if (post === '/tailnet/toggle') return true;
    if (elt.closest && elt.closest('[hx-post="/tailnet/toggle"],[data-hx-post="/tailnet/toggle"]')) {
      return true;
    }
  }
  var pathInfo = evt.detail.pathInfo;
  if (pathInfo && pathInfo.requestPath === '/tailnet/toggle') return true;
  return false;
}

function setTogglePending(isPending) {
  var toggleLabel = document.querySelector('.tailnet-toggle');
  if (!toggleLabel) return;
  var toggleInput = toggleLabel.querySelector('input[type="checkbox"]');
  if (toggleInput) {
    toggleInput.disabled = isPending;
  }
  toggleLabel.classList.toggle('is-pending', isPending);
  toggleLabel.setAttribute('aria-busy', isPending ? 'true' : 'false');
}


document.body.addEventListener('htmx:beforeRequest', function(evt) {
  if (!isToggleRequest(evt)) return;
  if (window.htmx && typeof window.htmx.pause === 'function') {
    window.htmx.pause();
  }
  setTogglePending(true);
});

document.body.addEventListener('htmx:afterRequest', function(evt) {
  if (!isToggleRequest(evt)) return;
  if (window.htmx && typeof window.htmx.resume === 'function') {
    window.htmx.resume();
  }
  setTogglePending(false);
});

document.body.addEventListener('htmx:responseError', function(evt) {
  if (!isToggleRequest(evt)) return;
  if (window.htmx && typeof window.htmx.resume === 'function') {
    window.htmx.resume();
  }
  setTogglePending(false);
});

// SSE connection for real-time updates
(function() {
  let eventSource = null;
  let reconnectTimeout = null;
  let reconnectDelay = 1000; // Start with 1 second

  function connect() {
    if (eventSource) {
      eventSource.close();
    }

    eventSource = new EventSource('/events');
    
    eventSource.addEventListener('connected', function() {
      console.log('SSE connected');
      reconnectDelay = 1000; // Reset delay on successful connection
    });

    eventSource.addEventListener('update', function(e) {
      console.log('SSE update received:', e.data);
      
      // Parse the event data (e.g., "tailnet-1" or "global")
      const data = e.data;
      
      if (data === 'global') {
        // Global change - refresh entire container
        window.htmx.ajax('GET', '/', {
          target: '#tailnets-container',
          swap: 'morph',
          select: '#tailnets-container'
        });
      } else if (data.startsWith('tailnet-')) {
        // Specific tailnet change - refresh entire card
        const tailnetID = data.replace('tailnet-', '');
        const card = document.getElementById('tailnet-card-' + tailnetID);
        
        if (card) {
          window.htmx.ajax('GET', '/', {
            target: '#tailnet-card-' + tailnetID,
            swap: 'morph',
            select: '#tailnet-card-' + tailnetID
          });
        }
      }
    });

    eventSource.onerror = function() {
      console.log('SSE error, reconnecting in', reconnectDelay, 'ms');
      eventSource.close();
      
      // Exponential backoff with max of 30 seconds
      reconnectTimeout = setTimeout(connect, reconnectDelay);
      reconnectDelay = Math.min(reconnectDelay * 2, 30000);
    };
  }

  // Start connection when page loads
  connect();

  // Clean up on page unload
  window.addEventListener('beforeunload', function() {
    if (reconnectTimeout) {
      clearTimeout(reconnectTimeout);
    }
    if (eventSource) {
      eventSource.close();
    }
  });
})();

