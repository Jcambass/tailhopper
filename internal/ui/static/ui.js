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

function switchTab(tailnetId, tabName) {
  // Hide all tab contents for this tailnet
  var tabContents = document.querySelectorAll('[id^="tab-"][id$="-' + tailnetId + '"]');
  tabContents.forEach(function(content) {
    content.classList.remove('active');
  });
  
  // Remove active class from all tab buttons for this tailnet
  var tabsContainer = document.getElementById('tabs-' + tailnetId);
  if (tabsContainer) {
    var tabButtons = tabsContainer.querySelectorAll('.tab-btn');
    tabButtons.forEach(function(btn) {
      btn.classList.remove('active');
    });
  }
  
  // Show selected tab content
  var selectedTab = document.getElementById('tab-' + tabName + '-' + tailnetId);
  if (selectedTab) {
    selectedTab.classList.add('active');
  }
  
  // Set active class on clicked button
  if (tabsContainer) {
    var clickedButton = Array.from(tabsContainer.querySelectorAll('.tab-btn')).find(function(btn) {
      return btn.textContent.toLowerCase() === tabName;
    });
    if (clickedButton) {
      clickedButton.classList.add('active');
    }
  }
}

