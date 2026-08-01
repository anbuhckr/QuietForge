// Global application state
export const state = {
  suggestions: [],
  _notifQueued: false,
  activeSuggestion: 0,
  pendingFolders: [],
  firstStatus: true,
  statusAbort: null,
  _inFlightRefresh: false,
  running: false,
  stopping: false,
  projectSelected: false,
  features: {},
  intentMode: 'build',
  totalTokens: { prompt: 0, completion: 0 },
  inputPricePerM: 2.50,
  outputPricePerM: 10.00,
  currentConversationId: null,
  uiRunTimeoutSeconds: 3600,
  currentArtifacts: [],
  currentArtifactWorkspace: 'global',
  runStartTime: 0,
  runTimerInterval: null,
  poll: null,
  sseSource: null
};

// Initialize features from localStorage
try {
  state.features = JSON.parse(localStorage.getItem('qf_features') || '{}');
  const mode = localStorage.getItem('qf_intent_mode');
  state.intentMode = (mode === 'auto' || !mode) ? 'build' : mode;
} catch(e) {}

export function mergedFeatures(defaults = {}) {
  return {
    ...(window.SERVER_FEATURES || {}),
    ...defaults,
    ...state.features
  };
}


