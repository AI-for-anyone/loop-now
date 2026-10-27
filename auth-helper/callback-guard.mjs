import http from 'node:http';
import { syncBuiltinESMExports } from 'node:module';

// Pi 1.1.0 swallows callback bind errors and offers manual fallback. In this
// one-operation process, observe only callback listeners at the fixed endpoint
// to report occupation and close owned sockets on abort. Never terminate peers.
export function guardCallbacks(signal, onOccupied, port = 1455) {
 const original = http.createServer;
 const owned = new Set();
 http.createServer = function (...args) {
  const server = original.apply(this, args);
  const listen = server.listen;
  server.listen = function (...params) {
   const first = params[0];
   const requested = typeof first === 'object' ? first?.port : first;
   if (Number(requested) === port) {
    owned.add(server);
    server.on('error', error => { if (error.code === 'EADDRINUSE') onOccupied(); });
   }
   return listen.apply(this, params);
  };
  return server;
 };
 syncBuiltinESMExports();
 const close = () => { for (const server of owned) { try { server.closeAllConnections(); server.close(); } catch {} } };
 signal.addEventListener('abort', close, { once: true });
 return () => { close(); signal.removeEventListener('abort', close); http.createServer = original; syncBuiltinESMExports(); };
}
