package main

import (
	"example.com/portless-e2e-store/wstest"
	"fmt"
	"net/http"
)

func websocketDependency(endpoint string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		socket, err := wstest.Dial(r.Context(), endpoint+"/ws")
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		defer socket.Close()
		if err = socket.WriteFrame(1, []byte("checkout to orders")); err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		_, payload, err := socket.ReadFrame()
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		writeJSON(w, 200, map[string]any{"websocket": string(payload)})
	}
}

func websocketPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; connect-src ws://"+r.Host)
	_, _ = fmt.Fprint(w, `<!doctype html><html lang="en"><title>WebSocket fixture</title>
<p id="text">Waiting for text</p><p id="binary">Waiting for binary</p><p id="protocol">Waiting for protocol</p><p id="closed">Waiting for close</p>
<script>
const endpoint = new URL('/api/ws?run=' + crypto.randomUUID(), location.href); endpoint.protocol = 'ws:';
const socket = new WebSocket(endpoint, ['portless-test']); socket.binaryType = 'arraybuffer';
socket.onopen = () => { document.querySelector('#protocol').textContent = socket.protocol; socket.send('hello websocket'); };
socket.onmessage = event => {
 if (typeof event.data === 'string') { document.querySelector('#text').textContent = event.data; socket.send(new Uint8Array([0, 1, 127, 128, 255])); }
 else { document.querySelector('#binary').textContent = Array.from(new Uint8Array(event.data)).join(','); socket.close(1000, 'complete'); }
};
socket.onclose = event => document.querySelector('#closed').textContent = 'Closed ' + event.code + ' clean=' + event.wasClean;
socket.onerror = () => document.querySelector('#closed').textContent = 'WebSocket failed';
</script></html>`)
}
