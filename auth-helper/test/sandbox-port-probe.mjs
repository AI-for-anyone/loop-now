// Child of sandbox-exec. Only the two parent-owned loopback ports are accepted.
import http from 'node:http';
const ports=process.argv.slice(2).map(Number);
if(ports.length!==2||ports.some(p=>!Number.isInteger(p)||p<1||p>65535))process.exit(2);
async function request(port){
 return new Promise(resolve=>{
  const req=http.get({host:'127.0.0.1',port,path:'/probe',timeout:1000},response=>{response.resume();response.on('end',()=>resolve({status:response.statusCode}));});
  req.on('timeout',()=>req.destroy(Object.assign(new Error('local probe timed out'),{code:'PROBE_TIMEOUT'})));
  req.on('error',error=>resolve({errorCode:typeof error.code==='string'?error.code:'PROBE_FAILED'}));
 });
}
process.stdout.write(JSON.stringify({allowed:await request(ports[0]),denied:await request(ports[1])})+'\n');
