import { $ } from '../../utils/dom.js?v=1785573007908';
import { highlightMentions, currentMentionQuery, fetchSuggestions, renderSuggestions, chooseSuggestion } from './Mentions.js?v=1785573007908';
import { send, updateSendStopButtons } from '../../core/engine.js?v=1785573007908';

export function textOfEditor() {
  return $('editor').value;
}

export function setEditorText(t) {
  $('editor').value = t;
  window.renderChips(t);
  resizeEditor();
  if (typeof updateBackdropHighlights === 'function') updateBackdropHighlights();
}

export function resizeEditor() {
  const e = $('editor');
  e.style.height = 'auto';
  e.style.height = Math.min(e.scrollHeight, 200) + 'px';
}
$('editor').addEventListener('scroll', () => {
  const backdrop = $('editorBackdrop');
  if (backdrop) backdrop.scrollTop = $('editor').scrollTop;
});

$('editor').addEventListener('input', async function() {
  this.style.height = 'auto';
  this.style.height = Math.min(this.scrollHeight, 200) + 'px';
  const backdrop = $('editorBackdrop');
  if (backdrop) {
    backdrop.style.height = 'auto';
    backdrop.style.height = Math.min(this.scrollHeight, 200) + 'px';
  }
  highlightMentions();
  updateSendStopButtons();
  const q = currentMentionQuery();
  if (q !== null) {
    window.suggestions = await fetchSuggestions(q);
    window.activeSuggestion = 0;
    renderSuggestions()
  } else {
    window.suggestions = [];
    renderSuggestions()
  }
});
$('editor').addEventListener('keydown', e => {
  if (e.key === 'Enter' && !e.shiftKey && !window.suggestions.length) {
    e.preventDefault();
    send()
  } else if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') {
    e.preventDefault();
    send()
  }
  if (window.suggestions.length) {
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      window.activeSuggestion = (window.activeSuggestion + 1) % window.suggestions.length;
      renderSuggestions()
    }
    if (e.key === 'ArrowUp') {
      e.preventDefault();
      window.activeSuggestion = (window.activeSuggestion - 1 + window.suggestions.length) % window.suggestions.length;
      renderSuggestions()
    }
    if (e.key === 'Enter') {
      e.preventDefault();
      chooseSuggestion(window.activeSuggestion)
    }
  }
});

window.textOfEditor = textOfEditor;
