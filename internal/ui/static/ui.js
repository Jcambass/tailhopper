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

var activeTabs = [];

document.addEventListener('htmx:beforeSwap', function(event) {
  if (event.detail.target && event.detail.target.id === 'tailnets-container') {
    activeTabs = Array.from(event.detail.target.querySelectorAll('.tab-input:checked')).map(function(input) {
      return input.id;
    });
  }
});

document.addEventListener('htmx:afterSwap', function(event) {
  if (event.detail.target && event.detail.target.id === 'tailnets-container') {
    activeTabs.forEach(function(id) {
      var input = document.getElementById(id);
      if (input) input.checked = true;
    });
  }
});

