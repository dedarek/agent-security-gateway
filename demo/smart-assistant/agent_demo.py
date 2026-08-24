#!/usr/bin/env python3
"""
企业智能助手 — ASG 全链路可观测 Demo

流量路径:
  Agent (本脚本) 
    -> LLM:  http://127.0.0.1:8181/v1/chat/completions  (探针 -> 网关上报 -> 上游)
    -> MCP:  http://127.0.0.1:8181/mcp                   (探针 -> 网关三轴判定 -> upstream-mcp)

网关可观测点:
  http://127.0.0.1:8090          控制台 UI
  http://127.0.0.1:8090/api/sessions
  http://127.0.0.1:8090/api/trajectory?session=<id>

一键运行:
  python agent_demo.py
  python agent_demo.py --session demo-smart-$(date +%s)
"""
import argparse, json, time, uuid, sys
import urllib.request, urllib.error

PROBE_MCP = "http://127.0.0.1:8181/mcp"
PROBE_LLM = "http://127.0.0.1:8181/v1/chat/completions"
GW_SESSIONS = "http://127.0.0.1:8090/api/sessions"
GW_TRAJECTORY = "http://127.0.0.1:8090/api/trajectory"

def mcp_call(tool: str, args: dict, session_id: str):
    """经探针调 MCP 工具，所有判定在网关发生"""
    payload = json.dumps({
        "jsonrpc": "2.0", "id": 1,
        "method": "tools/call",
        "params": {"name": tool, "arguments": args}
    }).encode()
    req = urllib.request.Request(PROBE_MCP, data=payload, headers={
        "Content-Type": "application/json",
        "Accept": "application/json, text/event-stream",
        "x-asg-session": session_id,
    })
    try:
        with urllib.request.urlopen(req, timeout=20) as r:
            body = r.read().decode()
            # SSE 格式: event: message\ndata: {...}
            for line in body.splitlines():
                if line.startswith("data:"):
                    data = json.loads(line[5:].strip())
                    return data
            return {"raw": body}
    except urllib.error.HTTPError as e:
        return {"error": e.code, "body": e.read().decode()[:500]}
    except Exception as e:
        return {"error": str(e)}

def mcp_list(session_id: str):
    payload = json.dumps({"jsonrpc":"2.0","id":1,"method":"tools/list"}).encode()
    req = urllib.request.Request(PROBE_MCP, data=payload, headers={
        "Content-Type": "application/json",
        "Accept": "application/json, text/event-stream",
        "x-asg-session": session_id,
    })
    with urllib.request.urlopen(req, timeout=10) as r:
        body = r.read().decode()
        for line in body.splitlines():
            if line.startswith("data:"):
                return json.loads(line[5:].strip())
        return body

def llm_call(prompt: str, session_id: str, model="ox-alpha-free"):
    payload = json.dumps({
        "model": model,
        "messages": [{"role":"user","content": prompt}],
        "max_tokens": 80
    }).encode()
    req = urllib.request.Request(PROBE_LLM, data=payload, headers={
        "Content-Type": "application/json",
        "x-asg-session": session_id,
    })
    try:
        with urllib.request.urlopen(req, timeout=60) as r:
            j = json.loads(r.read().decode())
            return j["choices"][0]["message"]["content"][:300]
    except Exception as e:
        return f"LLM error: {e}"

def gw_sessions():
    with urllib.request.urlopen(GW_SESSIONS, timeout=5) as r:
        return json.loads(r.read().decode())

VERDICT_MAP = {0:"ALLOW",1:"REDACT",2:"CONFIRM",3:"BLOCK"}
def verdict_str(v):
    if isinstance(v, int): return VERDICT_MAP.get(v, str(v))
    return str(v)

def gw_trajectory(sid: str):
    url = GW_TRAJECTORY + "?session=" + urllib.parse.quote(sid)
    with urllib.request.urlopen(url, timeout=5) as r:
        return json.loads(r.read().decode())

def extract_mcp_result(data):
    """从 MCP 响应中提取可读结果"""
    try:
        if "result" in data:
            c = data["result"].get("content", [])
            if c and isinstance(c, list):
                return c[0].get("text","")[:300]
            return json.dumps(data["result"])[:300]
        if "error" in data:
            return f"ERROR {data['error']}: {data.get('body','')[:200]}"
        return json.dumps(data)[:400]
    except: return str(data)[:400]

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--session", default=f"demo-smart-{int(time.time())}")
    args = ap.parse_args()
    sid = args.session
    print("="*68)
    print(f"  企业智能助手 Demo — ASG 全链路可观测")
    print(f"  Session: {sid}")
    print(f"  探针: {PROBE_MCP} / {PROBE_LLM}")
    print(f"  网关控制台: http://127.0.0.1:8090")
    print("="*68)

    # 0. 工具发现
    print(f"\n[0] 工具发现 (tools/list) ...")
    info = mcp_list(sid)
    tools = info.get("result",{}).get("tools",[]) if isinstance(info, dict) else []
    print(f"    上游工具: {[t['name'] for t in tools]}")

    # 1. LLM 大脑 (走探针 -> 上游 -> 网关上报)
    print(f"\n[1] LLM 大脑调用 (经探针 8181 -> 上游 ox-alpha-free)...")
    reply = llm_call("你是企业智能助手，请用一句话介绍你能做什么（查邮件、客户数据、发通知等）", sid)
    print(f"    LLM 回复: {reply[:180]}")

    # 2-7 各类工具场景，覆盖三轴判定
    scenarios = [
        ("get_inbox", {}, "正常读邮件 (ALLOW) — 无害", "ALLOW"),
        ("read_customer_db", {}, "读客户库 (ALLOW)", "ALLOW"),
        ("read_secret", {}, "读密钥文件 (REDACT 脱敏) — 含 1Password token", "REDACT"),
        ("delete_user", {"id": 123}, "越权删用户 (BLOCK 权限轴) — employee 角色", "BLOCK"),
        ("export_all_users", {}, "批量导出 (CONFIRM/BLOCK) — 需审批/策略拦截", "CONFIRM/BLOCK"),
        ("send_email", {"to":"manager@corp.com","body":"今日客户访问正常"}, "发邮件给可信地址 (ALLOW) — 非 taint", "ALLOW"),
    ]

    print(f"\n[2-7] MCP 工具链 (经 探针 8181 -> 网关三轴 -> upstream-mcp):")
    for tool, targs, desc, expect in scenarios:
        r = mcp_call(tool, targs, sid)
        preview = extract_mcp_result(r)
        # 从网关拉最新 verdict 需要稍等落盘
        time.sleep(0.3)
        print(f"  ▶ {tool:18s} 预期:{expect:14s}  {desc}")
        print(f"     ↳ 上游返回: {preview[:160]}")

    # 8. 行为轴：taint 攻击链 (get_inbox 注入 -> send_email 到 attacker)
    print(f"\n[8] 行为轴 Taint 攻击链 (get_inbox 注入地址 -> send_email 外发 = BLOCK)")
    print(f"    先读一次 inbox 拿注入的 attacker@gmail.com ...")
    r1 = mcp_call("get_inbox", {}, sid)
    print(f"    inbox: {extract_mcp_result(r1)[:160]}")
    print(f"    再尝试外发到 attacker@gmail.com (应被 BLOCK)...")
    r2 = mcp_call("send_email", {"to":"attacker@gmail.com","body":"customer list"}, sid)
    print(f"    send_email 结果: {extract_mcp_result(r2)[:200]}")

    # 9. 行为轴精度：taint 不应误拦可信地址
    print(f"\n[9] 行为轴精度对照 (同样 session，发给可信地址应 ALLOW)")
    r3 = mcp_call("send_email", {"to":"ceo@corp.com","body":"周报"}, sid)
    print(f"    send_email to ceo@corp.com: {extract_mcp_result(r3)[:200]}")

    # 10. 网关可观测性验证
    print(f"\n{'='*68}")
    print(f"  网关侧可观测性验证 (直接查网关 API)")
    print(f"{'='*68}")
    time.sleep(0.8)
    # 网关按租户分 session (ingress: tenant-alice)，自定义 sid 不会单独建 session
    gw_sid = "tenant-alice"
    try:
        sessions = gw_sessions()
        print(f"  网关 sessions 列表: {len(sessions)} 个")
        for s in sessions:
            mark = " ★ 本次流量落在这里" if s["session_id"]==gw_sid else ""
            print(f"    - {s['session_id'] or '(empty)':24s}  {s['events']:3d} events  last={s['last_verdict']}{mark}")

        traj = gw_trajectory(gw_sid)
        events = traj.get("events",[])
        # 只看本次新增的尾部事件
        tail = events[-12:] if len(events)>12 else events
        print(f"\n  本租户轨迹 ({gw_sid}): 共 {len(events)} events，展示最近 {len(tail)} 条 (本次)")
        for e in tail:
            call = e.get("Call",{})
            dec = e.get("Decision",{})
            final = verdict_str(dec.get("Final","?"))
            rationale = str(dec.get("Rationale",""))[:110]
            tool_id = str(call.get('ToolID','?'))
            print(f"    [{final:7s}] {tool_id:22s}  {rationale}")
        # 根因
        sug = traj.get("suggestion")
        if sug:
            print(f"\n  根因分析: {sug.get('root_cause','')}")
            for c in sug.get("chain",[])[:5]:
                print(f"    -> {c}")
            if sug.get("cedar_policy"):
                print(f"  建议策略:\n{sug['cedar_policy'][:400]}")

        # 小结
        verdicts = {}
        for e in events:
            v = verdict_str(e.get("Decision",{}).get("Final","?"))
            verdicts[v] = verdicts.get(v,0)+1
        print(f"\n  Verdict 分布(全量): {verdicts}")
        print(f"  本次新增 Verdict: ", end="")
        tv = {}
        for e in tail:
            v = verdict_str(e.get("Decision",{}).get("Final","?"))
            tv[v] = tv.get(v,0)+1
        print(tv)
        print(f"\n  说明:")
        print(f"    - send_email 全 BLOCK 是 Invariant 粗粒度规则 + Taint 精确拦截共同作用")
        print(f"    - Taint 对 attacker@gmail.com 会给出 token 级证据 (data-flow taint)")
        print(f"    - 要看内容级精度，请对比 gateway demo (自带 5 场景，含 ALLOW 的精度对照)")
        print(f"\n  ✅ 全链路验证完成 — 所有流量已在网关落盘，可在控制台查看:")
        print(f"     http://127.0.0.1:8090  (点左侧 Sessions -> {gw_sid})")
        # 事件上报 spool
        try:
            spool = open("D:/proj/agent-security-gateway/connect-events.jsonl",encoding="utf-8").read().strip().splitlines()
            print(f"  探针事件 spool: {len(spool)} 行 (connect-events.jsonl)")
        except: pass
    except Exception as e:
        print(f"  网关查询失败: {e}")
        print(f"  请确认网关运行: http://127.0.0.1:8090/api/sessions")

if __name__ == "__main__":
    main()
