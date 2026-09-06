import { createSession } from './session.js';

const $ = (id) => document.getElementById(id);
const socketURL = new URL('/ws', location.href);
socketURL.protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
const session = createSession({ url: socketURL.href, onChange: render });
let previousHistory = '';
let previousMembers = '';
let previousLastMessage = '';
let wasWanted = false;
let priorPending;
let theme = matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';

function updateTheme() {
  document.documentElement.dataset.theme = theme;
  $('theme').textContent = theme === 'dark' ? 'Light theme' : 'Dark theme';
  $('theme').setAttribute('aria-label', 'Switch to ' + (theme === 'dark' ? 'light' : 'dark') + ' theme');
}
updateTheme();
$('theme').addEventListener('click', () => { theme = theme === 'dark' ? 'light' : 'dark'; updateTheme(); });
$('another-tab').href = location.href;

function element(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}
function avatar(person) {
  const node = element('span', 'avatar', person.name.slice(0, 1).toUpperCase());
  node.setAttribute('aria-hidden', 'true');
  return node;
}
function displayName(person, state) {
  const duplicate = [...state.members, ...state.history.map((item) => item.sender)].some((other) => other.name === person.name && other.id !== person.id);
  return person.name + (duplicate ? ' · #' + person.id : '');
}
function bottom() {
  $('transcript').scrollTop = $('transcript').scrollHeight;
  $('new-messages').hidden = true;
}
function render(state) {
  $('status').textContent = state.status;
  $('status').dataset.state = state.status;
  $('join-form').hidden = state.wanted;
  $('joined').hidden = !state.wanted;
  $('identity-name').textContent = state.name;
  $('member-count').textContent = state.members.length;
  $('no-members').hidden = state.members.length > 0;
  $('no-members').textContent = state.wanted ? 'Waiting for the room…' : 'Join to see who’s here.';
  const membersKey = JSON.stringify([state.members, state.self, state.history.map((item) => item.sender)]);
  if (membersKey !== previousMembers) {
    previousMembers = membersKey;
    $('members').replaceChildren(...state.members.map((person) => {
      const row = element('li', 'member');
      row.append(avatar(person), element('span', 'member-name', displayName(person, state)));
      if (person.id === state.self?.id) row.append(element('small', 'you', 'you'));
      return row;
    }));
  }
  const historyKey = JSON.stringify([state.generation, state.history, state.members]);
  if (historyKey !== previousHistory) {
    const lastMessage = state.generation + ':' + (state.history.at(-1)?.id || 0);
    const hasNewMessage = lastMessage !== previousLastMessage && state.history.length > 0;
    previousLastMessage = lastMessage;
    const transcript = $('transcript');
    const nearBottom = transcript.scrollHeight - transcript.clientHeight - transcript.scrollTop < 60;
    const anchor = [...$('messages').children].find((node) => node.offsetTop >= transcript.scrollTop);
    const anchorID = anchor?.dataset.id;
    const anchorOffset = anchor ? anchor.offsetTop - transcript.scrollTop : 0;
    previousHistory = historyKey;
    $('messages').replaceChildren(...state.history.map((item) => {
      const row = element('li', 'message-row');
      row.dataset.id = String(item.id);
      const content = element('div', 'message-content');
      const meta = element('div', 'message-meta');
      const time = element('time', '', new Date(item.at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }));
      time.dateTime = item.at;
      meta.append(element('strong', '', displayName(item.sender, state)), time);
      content.append(meta, element('p', 'message-text', item.text));
      row.append(avatar(item.sender), content);
      return row;
    }));
    $('empty').hidden = state.history.length > 0;
    if (nearBottom) bottom();
    else {
      const replacement = [...$('messages').children].find((node) => node.dataset.id === anchorID);
      if (replacement) transcript.scrollTop = replacement.offsetTop - anchorOffset;
      if (hasNewMessage) $('new-messages').hidden = false;
      if (!state.history.length) $('new-messages').hidden = true;
    }
  }
  const others = state.members.filter((person) => person.id !== state.self?.id && state.typing.includes(person.id));
  $('typing').textContent = others.length ? others.map((person) => displayName(person, state)).join(', ') + (others.length === 1 ? ' is typing…' : ' are typing…') : '';
  $('message').disabled = !state.wanted;
  $('message').placeholder = state.status === 'Connected' ? 'Say something to the room…' : state.wanted ? 'Your draft stays here while reconnecting…' : 'Join the room to send a message.';
  if ($('message').value !== state.draft) $('message').value = state.draft;
  $('send').disabled = state.status !== 'Connected' || Boolean(state.pending) || !state.draft.trim();
  $('pending').hidden = !state.pending;
  if (state.pending) {
    const status = state.pending.status;
    $('pending-label').textContent = { pending: 'Sending…', unconfirmed: 'Delivery unconfirmed', rejected: 'Message rejected' }[status];
    $('pending-text').textContent = state.pending.text;
    $('pending-help').textContent = status === 'unconfirmed' ? 'It may have arrived. Copying and sending it again could create a duplicate.' : status === 'rejected' ? 'Copy the text to revise it, then dismiss this submission.' : 'Waiting for the room to confirm receipt.';
    $('pending-actions').hidden = status === 'pending';
    if (priorPending !== state.pending.requestId) $('copy-status').textContent = '';
    priorPending = state.pending.requestId;
  }
  for (const [id, value] of [['notice', state.notice], ['error', state.error]]) { $(id).textContent = value; $(id).hidden = !value; }
  $('guidance').hidden = !state.guidance;
  $('notices').hidden = !state.notice && !state.error && !state.guidance;
  if (state.wanted !== wasWanted) {
    wasWanted = state.wanted;
    (state.wanted ? $('message') : $('name')).focus();
  }
}
$('join-form').addEventListener('submit', (event) => { event.preventDefault(); session.join($('name').value); });
$('leave').addEventListener('click', () => session.leave());
$('message').addEventListener('input', () => session.setDraft($('message').value));
$('message').addEventListener('keydown', (event) => {
  if (event.key === 'Enter' && !event.shiftKey && !event.isComposing) { event.preventDefault(); session.send(); }
});
$('compose-form').addEventListener('submit', (event) => { event.preventDefault(); session.send(); $('message').focus(); });
$('dismiss').addEventListener('click', () => session.dismiss());
$('copy').addEventListener('click', async () => {
  const text = session.snapshot().pending?.text;
  if (text === undefined) return;
  try { await navigator.clipboard.writeText(text); $('copy-status').textContent = 'Copied'; }
  catch { $('copy-status').textContent = 'Select the text above to copy it.'; }
});
$('new-messages').addEventListener('click', bottom);
$('transcript').addEventListener('scroll', () => {
  if ($('transcript').scrollHeight - $('transcript').clientHeight - $('transcript').scrollTop < 60) $('new-messages').hidden = true;
});
window.addEventListener('pagehide', () => session.suspend());
window.addEventListener('pageshow', (event) => { if (event.persisted) session.resume(); });
render(session.snapshot());
