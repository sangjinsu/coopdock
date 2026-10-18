#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../../.."
export GODOT_BIN="${GODOT_BIN:-/Applications/Godot.app/Contents/MacOS/Godot}"
# Python owns only the children it starts and bounds all waits, including cleanup.
python3 - "$@" <<'PY'
import json, os, pathlib, socket, subprocess, sys, time, urllib.request
root = pathlib.Path.cwd()
out = pathlib.Path(os.environ.get('GODOT_CHECKS_OUTPUT_DIR', str(root / 'work/godot-client/task-4')))
if not out.is_absolute(): out = root / out
out.mkdir(parents=True, exist_ok=True)
env = os.environ.copy()
for key in list(env):
    if key.startswith(('ROOM_', 'WS_', 'ADMIN_')):
        del env[key]
env.update(GAME_IDS='default', GOCACHE=str(root/'work/go-build'), GOMODCACHE=str(root/'work/go-mod'), GOTMPDIR=str(root/'work/tmp'), GOTOOLCHAIN='local')
(root/'work/tmp').mkdir(parents=True, exist_ok=True)
godot = env['GODOT_BIN']
mixed = '--mixed-native' in sys.argv
mode = 'mixed-native' if mixed else ('native' if '--native' in sys.argv else 'checks')
metadata = {'mode':mode, 'GAME_IDS':'default', 'children':[], 'commands':[]}
owned = []
streams = []
def run(cmd, name, timeout=60):
    log = out / (name+'.log')
    with log.open('w') as stream:
        proc = subprocess.Popen(cmd, env=env, stdout=stream, stderr=subprocess.STDOUT)
        owned.append(proc)
        metadata['children'].append({'pid':proc.pid,'kind':name})
        try:
            code = proc.wait(timeout=timeout)
        except subprocess.TimeoutExpired:
            raise RuntimeError(name+' timeout')
    metadata['commands'].append({'argv':cmd,'exit':code,'log':str(log)})
    text = log.read_text()
    print(name, 'exit', code, text[-1200:], flush=True)
    if code or 'SCRIPT ERROR:' in text or '\nERROR:' in text:
        raise RuntimeError(name+' failed')
def server(name, read_timeout=None):
    with socket.socket() as sock:
        sock.bind(('127.0.0.1',0)); port=sock.getsockname()[1]
    server_env=env.copy(); server_env['HTTP_ADDR']='127.0.0.1:'+str(port)
    if read_timeout: server_env['WS_READ_TIMEOUT']=read_timeout
    stream=(out/(name+'-server.log')).open('w'); streams.append(stream)
    proc=subprocess.Popen([str(out/'coopdock-server')],env=server_env,stdout=stream,stderr=subprocess.STDOUT)
    owned.append(proc); metadata['children'].append({'pid':proc.pid,'kind':name+'-server','port':port,'WS_READ_TIMEOUT':read_timeout or 'default'})
    deadline=time.monotonic()+8
    while time.monotonic()<deadline:
        if proc.poll() is not None: raise RuntimeError('owned server exited early')
        try:
            with urllib.request.urlopen('http://127.0.0.1:'+str(port)+'/healthz', timeout=.2) as response:
                if response.status==200: return proc, 'ws://127.0.0.1:'+str(port)+'/ws'
        except OSError: pass
        time.sleep(.05)
    raise RuntimeError('health deadline')
def stop(proc):
    if proc.poll() is None:
        proc.terminate()
        try: proc.wait(timeout=5)
        except subprocess.TimeoutExpired:
            proc.kill(); proc.wait(timeout=3)
try:
    run(['go','build','-o',str(out/'coopdock-server'),'./cmd/server'],'server-build',120)
    if mode=='checks':
        if '--actual-only' not in sys.argv and '--heartbeat-only' not in sys.argv:
            run([godot,'--headless','--path','examples/godot','--import'],'import-final')
            run([godot,'--headless','--path','examples/godot','--script','res://tests/run_unit.gd'],'unit-final')
        if '--heartbeat-only' not in sys.argv:
            proc,url=server('integration')
            run([godot,'--headless','--path','examples/godot','--script','res://tests/run_integration.gd','--','--url',url],'integration',45)
            stop(proc)
        proc,url=server('heartbeat','3s')
        run([godot,'--headless','--path','examples/godot','--script','res://tests/run_integration.gd','--','--url',url,'--heartbeat-only'],'heartbeat',15)
        stop(proc)
    else:
        proc,url=server('native')
        room=out/('native-room-'+str(os.getpid())+'.json')
        native=[]
        roles = [('host','40,80'),('peer','620,120')]
        if mixed: roles += [('seed'+str(i),'0,0') for i in range(4)]
        for role,position in roles:
            prefix=out/('native-'+role)
            cmd=[godot,'--path','examples/godot','--rendering-method','gl_compatibility','--windowed','--resolution','1100x720','--position',position,'--script','res://tests/run_render_qa.gd','--','--url',url,'--mode',role,'--room-file',str(room),'--output-prefix',str(prefix)]
            if mixed:
                cmd += ['--mixed']
                if role.startswith('seed'): cmd.insert(1, '--headless')
            stream=(out/('native-'+role+'.log')).open('w');streams.append(stream)
            child=subprocess.Popen(cmd,env=env,stdout=stream,stderr=subprocess.STDOUT)
            owned.append(child); native.append((role,child,cmd))
            metadata['children'].append({'pid':child.pid,'kind':'native-'+role})
        deadline=time.monotonic()+(90 if mixed else 65)
        for role,child,cmd in native:
            code=child.wait(timeout=max(1,deadline-time.monotonic()))
            metadata['commands'].append({'argv':cmd,'exit':code})
            text=(out/('native-'+role+'.log')).read_text()
            print(role,'exit',code,text[-2000:],flush=True)
            if code or 'SCRIPT ERROR:' in text or '\nERROR:' in text: raise RuntimeError('native '+role+' failed')
        captures=[json.loads((out/('native-'+role+'-captures.json')).read_text()) for role,_ in roles]
        common=set.intersection(*(set(c) for c in captures))
        assert common, 'native no common capture'
        assert all(all(c[k]==captures[0][k] for c in captures) for k in common), 'native full common capture mismatch'
        metadata['native_common_full_captures']=len(common)
        metadata['native_window_count']=2
        metadata['socket_count']=len(roles)
        for role,_ in roles:
            report=json.loads((out/('native-'+role+'-report.json')).read_text())
            assert report['failures']==0
        stop(proc)
finally:
    for proc in reversed(owned): stop(proc)
    for stream in streams: stream.close()
    for child in metadata['children']:
        proc=next(p for p in owned if p.pid==child['pid'])
        child.update(exit=proc.returncode,reaped=proc.poll() is not None)
    (out/('runner-'+mode+'.json')).write_text(json.dumps(metadata,indent=2))
    print('cleanup',[(x['pid'],x['reaped']) for x in metadata['children']],flush=True)
PY
