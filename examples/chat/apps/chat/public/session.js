const protocol = 'portless-chat.v1';
const terminalCodes = new Set([1002, 1003, 1007, 1008, 1009]);
const encoder = new TextEncoder();
const member = (value) => value && typeof value.id === 'string' && typeof value.name === 'string' && value.name.length <= 24;
const message = (value) => value && value.type === 'message' && Number.isSafeInteger(value.id) && value.id > 0
  && typeof value.generation === 'string' && member(value.sender) && typeof value.at === 'string'
  && typeof value.text === 'string' && value.text.length <= 2000 && typeof value.requestId === 'string';

// The tab owns connection attempts and delivery state; rendering never owns timers.
export function createSession({
  url, onChange = () => {}, createSocket = (address, selected) => new WebSocket(address, selected),
  clock = globalThis, now = () => Date.now(), random = Math.random, requestId = () => crypto.randomUUID(),
}) {
  const state = {
    status: 'Disconnected', wanted: false, name: '', draft: '', pending: null,
    generation: null, self: null, members: [], typing: [], history: [],
    error: '', notice: '', guidance: false,
  };
  let socket;
  let epoch = 0;
  let retries = 0;
  let resumeWanted = false;
  let stageTimer, retryTimer, confirmationTimer, guidanceTimer, typingTimer, typingPulse;
  let lastTyping = -Infinity;
  const snapshot = () => ({ ...state, members: [...state.members], typing: [...state.typing], history: [...state.history], pending: state.pending && { ...state.pending } });
  const emit = () => onChange(snapshot());
  const cancel = (timer) => { if (timer !== undefined) clock.clearTimeout(timer); };
  const unresolved = () => {
    if (state.pending?.status === 'pending') state.pending = { ...state.pending, status: 'unconfirmed' };
  };
  function clearAttempt() {
    ++epoch;
    cancel(stageTimer); cancel(confirmationTimer); cancel(typingTimer); cancel(typingPulse);
    stageTimer = confirmationTimer = typingTimer = typingPulse = undefined;
    const previous = socket;
    socket = undefined;
    if (previous && previous.readyState < 2) previous.close(1000);
    unresolved();
    state.members = []; state.typing = []; state.self = null;
  }
  function guide() {
    if (guidanceTimer !== undefined) return;
    guidanceTimer = clock.setTimeout(() => { state.guidance = true; emit(); }, 15_000);
  }
  function lost(code) {
    clearAttempt();
    if (terminalCodes.has(code)) {
      state.wanted = false;
      state.guidance = false;
      const explanation = {
        1002: 'The room connection used an unsupported protocol. Join again.',
        1003: 'The room sent an unsupported message. Join again.',
        1007: 'The room sent invalid text. Join again.',
        1009: 'A message exceeded the connection limit. Join again.',
      }[code];
      state.error = explanation || state.error || 'The connection was rejected. Check your name and join again.';
    }
    if (!state.wanted) {
      state.status = 'Disconnected';
      cancel(guidanceTimer); guidanceTimer = undefined;
    } else {
      state.status = 'Reconnecting';
      guide();
      const delay = Math.min(5000, 500 * (2 ** Math.min(retries++, 4))) * (0.8 + random() * 0.2);
      cancel(retryTimer);
      retryTimer = clock.setTimeout(() => { retryTimer = undefined; attempt(); }, delay);
    }
    emit();
  }
  function watch() {
    cancel(stageTimer);
    stageTimer = clock.setTimeout(() => lost(1011), 10_000);
  }
  function transmit(value) {
    if (!socket || socket.readyState !== 1) return false;
    try { socket.send(JSON.stringify(value)); return true; } catch { lost(1011); return false; }
  }
  function confirms(value) {
    return state.pending && value.generation === state.pending.generation
      && value.sender.id === state.pending.memberId && value.requestId === state.pending.requestId;
  }
  function accept(value, phase) {
    if (value?.type === 'error' && typeof value.code === 'string' && typeof value.message === 'string' && value.message.length <= 300) {
      state.error = value.message;
      if (value.code === 'INVALID_TEXT' && state.pending?.requestId === value.requestId) {
        cancel(confirmationTimer); confirmationTimer = undefined;
        state.pending = { ...state.pending, status: 'rejected' };
      }
      emit();
      return phase;
    }
    if (phase === 'ready' && value?.type === 'ready') {
      state.status = 'Joining';
      watch();
      transmit({ type: 'join', name: state.name });
      emit();
      return 'welcome';
    }
    if (phase === 'welcome' && value?.type === 'welcome' && value.room === 'lobby'
      && typeof value.generation === 'string' && member(value.self)
      && Array.isArray(value.members) && value.members.length <= 32 && value.members.every(member)
      && Array.isArray(value.typing) && value.typing.length <= 32 && value.typing.every((id) => typeof id === 'string')
      && Array.isArray(value.history) && value.history.length <= 50 && value.history.every((item) => message(item) && item.generation === value.generation)) {
      const last = state.history.at(-1)?.id;
      if (state.generation && state.generation !== value.generation) state.notice = 'Room restarted; history cleared.';
      else if (last && value.history[0]?.id > last + 1) state.notice = 'Some older messages are no longer available.';
      state.generation = value.generation;
      state.self = value.self; state.members = value.members; state.typing = value.typing; state.history = value.history;
      if (value.history.some(confirms)) state.pending = null;
      state.status = 'Connected'; state.error = ''; state.guidance = false;
      cancel(stageTimer); cancel(guidanceTimer); stageTimer = guidanceTimer = undefined;
      retries = 0; lastTyping = -Infinity;
      emit();
      return 'connected';
    }
    if (phase === 'connected' && value?.generation === state.generation) {
      if (message(value)) {
        if (confirms(value)) {
          state.pending = null;
          cancel(confirmationTimer); confirmationTimer = undefined;
        }
        if (value.id > (state.history.at(-1)?.id || 0)) state.history = [...state.history, value].slice(-50);
      } else if (value.type === 'presence' && Array.isArray(value.members) && value.members.length <= 32 && value.members.every(member)) state.members = value.members;
      else if (value.type === 'typing' && Array.isArray(value.memberIds) && value.memberIds.length <= 32 && value.memberIds.every((id) => typeof id === 'string')) state.typing = value.memberIds;
      else { lost(1008); return 'closed'; }
      emit();
      return phase;
    }
    lost(1008);
    return 'closed';
  }
  function attempt() {
    if (!state.wanted) return;
    const token = ++epoch;
    let current;
    try { current = createSocket(url, protocol); } catch { lost(1011); return; }
    socket = current;
    let phase = 'opening';
    const active = () => token === epoch && socket === current && state.wanted;
    watch();
    current.addEventListener('open', () => {
      if (!active()) return;
      if (current.protocol !== protocol) { lost(1008); return; }
      phase = 'ready';
      watch();
    });
    current.addEventListener('message', (event) => {
      if (!active()) return;
      let value;
      if (typeof event.data !== 'string' || encoder.encode(event.data).length > 512 * 1024) { lost(1009); return; }
      try { value = JSON.parse(event.data); } catch { lost(1008); return; }
      phase = accept(value, phase);
    });
    current.addEventListener('close', (event) => { if (active()) lost(event.code); });
    // The close event or stage watchdog supplies recovery; browser errors hide HTTP status.
    current.addEventListener('error', () => {});
  }
  function stop() {
    state.wanted = false;
    cancel(retryTimer); cancel(guidanceTimer);
    retryTimer = guidanceTimer = undefined;
    clearAttempt();
    state.status = 'Disconnected'; state.guidance = false;
    emit();
  }
  return {
    snapshot,
    join(name) {
      if (state.wanted) return;
      const normalized = name.normalize('NFC').trim();
      if (!normalized || normalized.length > 24 || /[\u0000-\u001f\u007f-\u009f]/u.test(normalized)) {
        state.error = 'Choose a name between 1 and 24 characters without control characters.'; emit(); return;
      }
      state.name = normalized; state.wanted = true; state.error = ''; state.status = 'Connecting';
      retries = 0; guide(); emit(); attempt();
    },
    leave() { resumeWanted = false; stop(); },
    suspend() { resumeWanted = state.wanted; stop(); },
    resume() { const wanted = resumeWanted; resumeWanted = false; if (wanted) this.join(state.name); },
    setDraft(text) {
      state.draft = text;
      if (state.status === 'Connected') {
        cancel(typingTimer);
        cancel(typingPulse);
        const pulse = () => {
          typingPulse = undefined;
          if (state.status !== 'Connected') return;
          lastTyping = now();
          transmit({ type: 'typing', active: Boolean(state.draft) });
        };
        const remaining = Math.max(0, 500 - (now() - lastTyping));
        if (remaining) typingPulse = clock.setTimeout(pulse, remaining);
        else pulse();
        typingTimer = clock.setTimeout(() => {
          typingTimer = undefined;
          if (state.status === 'Connected') transmit({ type: 'typing', active: false });
        }, 3000);
      }
      emit();
    },
    send() {
      if (state.status !== 'Connected' || state.pending) return false;
      const text = state.draft;
      const id = requestId();
      const command = { type: 'message', requestId: id, text };
      if (!text.trim() || text.length > 2000 || encoder.encode(JSON.stringify(command)).length > 8192) {
        state.error = 'Keep your message within 2,000 characters and 8 KiB encoded.'; emit(); return false;
      }
      state.pending = { generation: state.generation, memberId: state.self.id, requestId: id, text, status: 'pending' };
      state.draft = ''; state.error = '';
      cancel(typingTimer); cancel(typingPulse); typingTimer = typingPulse = undefined;
      confirmationTimer = clock.setTimeout(() => {
        confirmationTimer = undefined; unresolved(); lost(1011);
      }, 5000);
      const sent = transmit(command);
      emit();
      return sent;
    },
    dismiss() {
      if (state.pending?.status === 'pending') return;
      state.pending = null; state.error = ''; emit();
    },
  };
}
