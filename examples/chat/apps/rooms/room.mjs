import { randomUUID } from 'node:crypto';

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;
const fields = (value, keys) => Object.keys(value).every((key) => keys.includes(key));

// One synchronous room owner keeps the welcome snapshot ordered with broadcasts.
export function createRoom() {
  const generation = randomUUID();
  const clients = new Map();
  const history = [];
  let nextMember = 0;
  let nextMessage = 0;

  const members = () => [...clients.values()].filter((client) => client.member).map((client) => client.member);
  const typing = () => [...clients.values()].filter((client) => client.typing).map((client) => client.member.id);
  const event = (type, data) => ({ type, generation, ...data });

  function send(client, value) {
    if (!clients.has(client.peer)) return false;
    if (client.peer.send(value)) return true;
    leave(client);
    return false;
  }

  function broadcast(value) {
    for (const client of [...clients.values()]) {
      if (client.member) send(client, value);
    }
  }

  function clearTyping(client) {
    clearTimeout(client.typingTimer);
    client.typingTimer = undefined;
    if (!client.typing) return;
    client.typing = false;
    broadcast(event('typing', { memberIds: typing() }));
  }

  function leave(client) {
    if (!clients.delete(client.peer)) return;
    clearTimeout(client.joinTimer);
    clearTimeout(client.typingTimer);
    if (client.member) {
      broadcast(event('presence', { members: members() }));
      if (client.typing) broadcast(event('typing', { memberIds: typing() }));
    }
  }

  function reject(client, code, message, close = true, requestId) {
    send(client, { type: 'error', code, message, ...(requestId ? { requestId } : {}) });
    if (close) {
      leave(client);
      client.peer.close(code === 'JOIN_TIMEOUT' ? 1013 : 1008);
    }
  }

  return {
    connect(peer) {
      const client = { peer, member: null, typing: false };
      clients.set(peer, client);
      client.joinTimer = setTimeout(() => reject(client, 'JOIN_TIMEOUT', 'Joining took too long. Please reconnect.'), 5000);
      return {
        receive(command) {
          if (!clients.has(peer)) return;
          if (!command || typeof command !== 'object' || Array.isArray(command)) {
            reject(client, 'INVALID_COMMAND', 'Use a supported chat command.');
            return;
          }
          if (command.type === 'join' && !client.member && fields(command, ['type', 'name'])) {
            const name = typeof command.name === 'string' ? command.name.normalize('NFC').trim() : '';
            if (!name || name.length > 24 || /[\u0000-\u001f\u007f-\u009f]/u.test(name)) {
              reject(client, 'INVALID_NAME', 'Choose a name between 1 and 24 characters without control characters.');
              return;
            }
            clearTimeout(client.joinTimer);
            client.member = { id: String(++nextMember), name };
            if (send(client, event('welcome', { room: 'lobby', self: client.member, members: members(), typing: typing(), history: [...history] }))) {
              broadcast(event('presence', { members: members() }));
            }
            return;
          }
          if (client.member && command.type === 'message' && fields(command, ['type', 'requestId', 'text']) && typeof command.requestId === 'string' && uuid.test(command.requestId)) {
            if (typeof command.text !== 'string' || !command.text.trim() || command.text.length > 2000) {
              reject(client, 'INVALID_TEXT', 'Write a message between 1 and 2,000 characters.', false, command.requestId);
              return;
            }
            const message = event('message', { id: ++nextMessage, sender: client.member, at: new Date().toISOString(), text: command.text, requestId: command.requestId });
            history.push(message);
            if (history.length > 50) history.shift();
            broadcast(message);
            clearTyping(client);
            return;
          }
          if (client.member && command.type === 'typing' && fields(command, ['type', 'active']) && typeof command.active === 'boolean') {
            if (!command.active) clearTyping(client);
            else {
              clearTimeout(client.typingTimer);
              const changed = !client.typing;
              client.typing = true;
              client.typingTimer = setTimeout(() => clearTyping(client), 3000);
              if (changed) broadcast(event('typing', { memberIds: typing() }));
            }
            return;
          }
          reject(client, 'INVALID_COMMAND', 'Join once, then use supported chat commands.');
        },
        leave: () => leave(client),
      };
    },
    dispose() {
      for (const client of clients.values()) {
        clearTimeout(client.joinTimer);
        clearTimeout(client.typingTimer);
      }
      clients.clear();
    },
  };
}
