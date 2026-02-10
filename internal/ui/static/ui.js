function copyToClipboard(elementId) {
  var el = document.getElementById(elementId);
  if (!el) return;
  
  var text = el.textContent || el.innerText;
  navigator.clipboard.writeText(text).then(function() {
    // Show brief feedback by swapping icons
    var btn = el.closest('.code-block').querySelector('.copy-btn');
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

