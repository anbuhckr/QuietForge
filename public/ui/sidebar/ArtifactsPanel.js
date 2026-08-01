import { $, esc, md } from '../../utils/dom.js?v=1785573007908';

export let currentArtifacts = [];
export let currentArtifactWorkspace = '';
export let currentArtifactPath = '';
export let currentArtifactRaw = '';

export function artifactKey(suffix) {
  return `qf_artifacts_${suffix}:${currentArtifactWorkspace || 'global'}`
}

export function artifactDismissed() {
  return localStorage.getItem(artifactKey('dismissed')) === '1'
}

export function setArtifactDismissed(value) {
  value ? localStorage.setItem(artifactKey('dismissed'), '1') : localStorage.removeItem(artifactKey('dismissed'))
}

export function artifactSeenCount() {
  return parseInt(localStorage.getItem(artifactKey('seen_count')) || '0', 10) || 0
}

export function setArtifactSeenCount(count) {
  localStorage.setItem(artifactKey('seen_count'), String(Math.max(0, count)))
}

export function updateArtifactsChrome() {
  const count = currentArtifacts.length;
  if ($('artifactCountBadge')) $('artifactCountBadge').textContent = String(count);
  if ($('artifactToggleCount')) $('artifactToggleCount').textContent = String(count);
  if ($('artifactToggleBtn')) $('artifactToggleBtn').style.display = count ? 'inline-flex' : 'none';
  if (!count) closeArtifactsOverlay();
}

export function openArtifactsOverlay(mode = 'list') {
  if (mode === 'list' && (!currentArtifacts || !currentArtifacts.length)) return;
  const overlay = $('artifactOverlay');
  if (!overlay) return;
  overlay.classList.add('open');
  overlay.setAttribute('aria-hidden', 'false');
  document.body.classList.add('artifact-open');
  if (mode === 'list') {
    if ($('artifactViewer')) $('artifactViewer').style.display = 'none';
    if ($('artifactsSidebar')) $('artifactsSidebar').style.display = 'flex';
  }
}

export function closeArtifactsOverlay() {
  const overlay = $('artifactOverlay');
  if (!overlay) return;
  if (overlay.contains(document.activeElement)) {
    let restoreTarget = $('artifactToggleBtn');
    if (!restoreTarget || restoreTarget.offsetWidth === 0) {
      restoreTarget = $('editor');
    }
    if (restoreTarget && typeof restoreTarget.focus === 'function') {
      restoreTarget.focus({ preventScroll: true });
    } else if (document.activeElement && typeof document.activeElement.blur === 'function') {
      document.activeElement.blur();
    }
  }
  overlay.classList.remove('open');
  overlay.setAttribute('aria-hidden', 'true');
  document.body.classList.remove('artifact-open');
}

export function setupTabs() {
  const backBtn = $('artifactBackBtn');
  if (backBtn) backBtn.onclick = () => {
    if ($('artifactViewer')) $('artifactViewer').style.display = 'none';
    if ($('artifactsSidebar')) $('artifactsSidebar').style.display = 'flex';
  };
  if ($('artifactsCloseBtn')) $('artifactsCloseBtn').onclick = () => {
    closeArtifactsOverlay();
    setArtifactDismissed(true);
    setArtifactSeenCount(currentArtifacts.length);
    updateArtifactsChrome();
  };
  if ($('artifactToggleBtn')) $('artifactToggleBtn').onclick = () => {
    setArtifactDismissed(false);
    setArtifactSeenCount(currentArtifacts.length);
    updateArtifactsChrome();
    openArtifactsOverlay('list');
  };
  if ($('artifactOverlay')) $('artifactOverlay').addEventListener('click', e => {
    if (e.target === $('artifactOverlay')) closeArtifactsOverlay();
  });
}

export function renderArtifacts(artifacts, workspace = 'global') {
  currentArtifactWorkspace = String(workspace || 'global');
  artifacts = Array.isArray(artifacts) ? artifacts : [];
  const unchanged = JSON.stringify(artifacts) === JSON.stringify(currentArtifacts);
  currentArtifacts = artifacts || [];
  if (artifacts.length > artifactSeenCount()) {
    setArtifactSeenCount(artifacts.length);
  } else if (!artifacts.length) {
    setArtifactSeenCount(0);
  }
  updateArtifactsChrome();
  if (unchanged) return;

  const list = $('artifactsSidebar');
  if (!list) return;
  list.innerHTML = '';

  if (currentArtifacts.length === 0) {
    return;
  }

  const diffArtifacts = currentArtifacts.filter(isDiffArtifact).reverse();
  const otherArtifacts = currentArtifacts.filter(art => !isDiffArtifact(art)).reverse();
  if (diffArtifacts.length) renderDiffReviewWidget(list, diffArtifacts);
  renderPlainArtifactList(list, otherArtifacts, diffArtifacts.length ? 'Other artifacts' : 'Artifacts');
}

export function viewArtifact(art) {
  openArtifactsOverlay('viewer');
  $('artifactsSidebar').style.display = 'none';
  const viewer = $('artifactViewer');
  viewer.style.display = 'flex';
  $('artifactTitle').textContent = art.title;

  const actions = $('artifactActions');
  if (actions) {
    if (art.isWorkspaceFile) {
      actions.style.display = 'flex';
      $('artifactEditBtn').style.display = 'block';
      $('artifactSaveBtn').style.display = 'none';
      currentArtifactPath = art.path;
      currentArtifactRaw = art.rawContent;
    } else {
      actions.style.display = 'none';
    }
  }

  const body = $('artifactBody');
  body.classList.remove('editing');
  body.classList.toggle('artifact-doc-body', isMarkdownArtifact(art));
  if (art.html) {
    body.innerHTML = DOMPurify.sanitize(art.html);
  } else if (window.marked) {
    body.innerHTML = md(art.content);
  } else {
    body.innerHTML = '<pre>' + esc(art.content) + '</pre>';
  }
}

