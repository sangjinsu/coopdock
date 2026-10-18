const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

class Element {
  constructor(tagName = 'DIV') { this.tagName = tagName; this.value = ''; this.listeners = {}; this.children = []; this.attributes = {}; this.disabled = false; this.checked = true; this.dataset = {}; this.classList = {toggle() {}}; this._text = ''; }
  set textContent(value) { this._text = String(value); this.children = []; }
  get textContent() { return this._text + this.children.map(child => child.textContent).join(''); }
  setAttribute(name, value) { this.attributes[name] = value; }
  append(child) { this.children.push(child); }
  replaceChildren(...children) { this.children = children; this._text = ''; }
  addEventListener(name, fn) { this.listeners[name] = fn; }
  emit(name) { if (!this.disabled) this.listeners[name]?.({}); }
}
const settle = async () => { for (let i = 0; i < 30; i++) await Promise.resolve(); };
function fixture() {
  const html = fs.readFileSync(path.join(__dirname, 'web/index.html'), 'utf8');
  const elements = new Map([...html.matchAll(/<([a-z]+)\b[^>]*\bid="([^"]+)"/g)].map(([,tag,id]) => [id, new Element(tag.toUpperCase())]));
  for (const id of ['phase','visibility']) elements.get(id).value = 'all';
  const calls = [];
  let mode = 'ok';
  const at = '2026-10-18T00:00:00Z';
  const common = {age_ms:0,generation:1,stale:false,updated_at:at};
  const rows = [{game_id:'game-a', room_id:'a',room_number:'004271'}, {game_id:'game-b',room_id:'b',room_number:'004271'}].map(row => ({...common,...row,host_id:'host',visibility:'public',phase:'WAITING',capacity:6,member_count:1,match_id:null}));
  const status = {...common,server_instance_id:'server',state:'RUNNING',ready:true,started_at:at,active_connections:2,room_member_count:2,room_count:2,waiting_room_count:2,playing_room_count:0,uptime_seconds:0,heap_alloc_bytes:0,goroutines:0,cpu_percent:null,rtt_ms:null,client_display_delay_ms:null};
  const document = {getElementById:id => elements.get(id), createElement:tag => new Element(tag.toUpperCase()), hidden:false, body:new Element(),addEventListener(){}};
  const fetch = async (url) => {
    calls.push(url);
    if (url.includes('/status')) return {status:200,ok:true,json:async()=>status};
    const q = new URL(url,'http://localhost').searchParams;
    let selected = rows.filter(row => !q.has('game_id') || row.game_id === q.get('game_id'));
    if (mode === 'missing-game') selected = selected.map(({game_id,...row}) => row);
    if (mode === 'invalid-game') selected = selected.map(row => ({...row,game_id:'Bad'}));
    if (mode === 'auth') return {status:401,ok:false};
    return {status:200,ok:true,json:async()=>({...common,total:2,matched_total:selected.length,limit:100,offset:0,rooms:selected})};
  };
  vm.runInNewContext(fs.readFileSync(path.join(__dirname,'web/dashboard.js'),'utf8'), {document,window:{addEventListener(){}},location:{reload(){}},fetch,URLSearchParams,AbortController,performance:{now:()=>0},Intl,Date,setTimeout:()=>1,clearTimeout(){}});
  return {elements,calls,setMode:value=>{mode=value;}};
}

test('exact game filter selects game rows and clear omits empty game query', async () => {
  const f=fixture(); await settle();
  const rows=f.elements.get('room-rows');
  assert.equal(rows.children.length,2);
  assert.match(rows.children[0].textContent,/game-a/);
  assert.match(rows.children[1].textContent,/game-b/);
  const input=f.elements.get('game-id'); assert.ok(input,'game ID filter is available');
  input.value='game-a'; input.emit('input'); await settle();
  assert.equal(new URL(f.calls.at(-1),'http://localhost').searchParams.get('game_id'),'game-a');
  assert.equal(rows.children.length,1);
  assert.match(f.elements.get('displayed-query').textContent,/game-a/);
  f.elements.get('clear-filters').emit('click'); await settle();
  assert.equal(input.value,'');
  assert.equal(new URL(f.calls.at(-1),'http://localhost').searchParams.has('game_id'),false);
  assert.equal(rows.children.length,2);
});
test('invalid game input prevents fetch, invalid response retains rows, auth clears filter', async () => {
  const f=fixture(); await settle(); const input=f.elements.get('game-id'); assert.ok(input,'game ID filter is available');
  const count=f.calls.length; input.value='Game-A';input.emit('input');await settle();
  assert.equal(f.calls.length,count);assert.equal(input.attributes['aria-invalid'],'true');assert.match(f.elements.get('game-error').textContent,/game_id|게임/);
  input.value='game-a';input.emit('input');await settle();
  for (const mode of ['missing-game','invalid-game']) {f.setMode(mode);f.elements.get('refresh').emit('click');await settle();assert.equal(f.elements.get('room-rows').children.length,1);assert.match(f.elements.get('rooms-error').textContent,/갱신 실패/);}
  f.setMode('auth');f.elements.get('refresh').emit('click');await settle();
  assert.equal(input.disabled,true);assert.equal(input.value,'');assert.equal(f.elements.get('room-rows').children.length,0);
});
