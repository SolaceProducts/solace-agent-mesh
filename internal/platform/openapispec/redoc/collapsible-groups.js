// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

(function () {
  const GROUP = 'li[data-item-id^="group/"]';
  const GROUP_LABEL = GROUP + ' > label';

  function setExpanded(group, expanded) {
    if (expanded) group.dataset.expanded = 'true';
    else delete group.dataset.expanded;
    syncAriaExpanded(group);
  }

  // Redoc renders aria-expanded="true" on every group whatever its state here.
  function syncAriaExpanded(group) {
    const expanded = group.dataset.expanded === 'true' ? 'true' : 'false';
    if (group.getAttribute('aria-expanded') !== expanded) group.setAttribute('aria-expanded', expanded);
  }

  function toggle(group) {
    setExpanded(group, group.dataset.expanded !== 'true');
  }

  function followActiveItem() {
    const active = document.querySelector(GROUP + ' label.active');
    const activeGroup = active && active.closest(GROUP);
    document.querySelectorAll(GROUP).forEach(function (group) {
      const holdsActive = group === activeGroup;
      if (holdsActive) group.dataset.hasActive = 'true';
      else delete group.dataset.hasActive;
      setExpanded(group, holdsActive);
    });
  }

  // Stopped before Redoc's own handlers, which treat activating a group as
  // activating the current operation and scroll the content pane back to it.
  document.addEventListener('click', function (event) {
    const label = event.target.closest && event.target.closest(GROUP_LABEL);
    if (!label) return;
    event.stopPropagation();
    toggle(label.parentElement);
  }, true);

  document.addEventListener('keydown', function (event) {
    if (event.key !== 'Enter' && event.key !== ' ') return;
    if (!(event.target.matches && event.target.matches(GROUP))) return;
    event.preventDefault();
    event.stopPropagation();
    toggle(event.target);
  }, true);

  let lastActive = null;
  function onSidebarChange() {
    const active = document.querySelector(GROUP + ' label.active');
    if (active !== lastActive) {
      lastActive = active;
      followActiveItem();
      return;
    }
    document.querySelectorAll(GROUP).forEach(syncAriaExpanded);
  }

  new MutationObserver(onSidebarChange).observe(document.body, {
    attributes: true,
    childList: true,
    subtree: true,
    attributeFilter: ['class', 'aria-expanded'],
  });
  onSidebarChange();
})();
