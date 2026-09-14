(function () {
  function enhance(pre) {
    if (pre.dataset.copyEnhanced === 'true') {
      return;
    }
    pre.dataset.copyEnhanced = 'true';

    var wrapper = document.createElement('div');
    wrapper.className = 'copy-wrapper';
    pre.parentNode.insertBefore(wrapper, pre);
    wrapper.appendChild(pre);

    var button = document.createElement('button');
    button.type = 'button';
    button.className = 'copy-button';
    button.textContent = 'Copy';

    button.addEventListener('click', function () {
      var text = pre.innerText.replace(/\n+$/, '');
      // Older browsers, and any page served over plain http, have no
      // navigator.clipboard at all.
      var done = navigator.clipboard
        ? navigator.clipboard.writeText(text)
        : Promise.reject();

      done.then(
        function () {
          button.textContent = 'Copied';
        },
        function () {
          button.textContent = 'Press Ctrl+C';
          var range = document.createRange();
          range.selectNodeContents(pre);
          var sel = window.getSelection();
          sel.removeAllRanges();
          sel.addRange(range);
        }
      );

      setTimeout(function () {
        button.textContent = 'Copy';
      }, 2000);
    });

    wrapper.appendChild(button);
  }

  function run() {
    document.querySelectorAll('pre').forEach(enhance);
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', run);
  } else {
    run();
  }
})();
