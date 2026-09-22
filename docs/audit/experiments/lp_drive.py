import json,subprocess,time,sys,os,threading
W=os.environ.get("AUDIT_DIR","/tmp/leanproxy-audit")
def tok(s): return len(s)//4
env=dict(os.environ,HOME=W+"/home"); os.makedirs(W+"/home",exist_ok=True)
p=subprocess.Popen([os.environ.get("LEANPROXY_BIN","leanproxy-mcp"),"server","run","--stdio","--config",W+"/cat.yaml","--log-file",W+"/lp.log"],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True,env=env)
def rpc(i,m,params=None):
    msg={"jsonrpc":"2.0","id":i,"method":m}
    if params is not None: msg["params"]=params
    t=time.perf_counter(); p.stdin.write(json.dumps(msg)+"\n"); p.stdin.flush()
    out=p.stdout.readline(); return out,(time.perf_counter()-t)*1000
t0=time.perf_counter()
o,ms=rpc(1,"initialize",{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}})
print("initialize ms=%.0f"%ms, o[:160])
p.stdin.write('{"jsonrpc":"2.0","method":"notifications/initialized"}\n');p.stdin.flush()
o,ms=rpc(2,"tools/list",{}); r=json.loads(o)["result"]
print("tools/list: n=%d tokens=%d ms=%.0f"%(len(r["tools"]),tok(json.dumps(r)),ms), [t["name"] for t in r["tools"]])
def call(i,name,args):
    o,ms=rpc(i,"tools/call",{"name":name,"arguments":args}); return o,ms
o,ms=call(3,"list_servers",{}); print("list_servers tokens=%d ms=%.0f"%(tok(o),ms)); print(o[:600])
res={}
for i,s in enumerate(["github","jira","slack","garmin","postgres"]):
    o,ms=call(10+i,"list_tools",{"server_name":s}); res[s]=tok(json.loads(o).get("result",{}).__str__()) if o else -1
    print("list_tools",s,"tokens=%d ms=%.0f"%(tok(o),ms))
    if s=="github": print(o[:900])
o,ms=call(30,"invoke_tool",{"server":"github","tool":"create_issue","arguments":{"owner":"a","repo":"b","title":"t"}})
print("invoke_tool tokens=%d ms=%.0f"%(tok(o),ms)); print(o[:500])
# latency distribution
lat=[]
for k in range(200):
    o,ms=call(100+k,"invoke_tool",{"server":"slack","tool":"slack_post_message","arguments":{"channel_id":"c","text":"hi"}}); lat.append(ms)
lat.sort(); print("invoke_tool latency p50=%.2fms p95=%.2fms p99=%.2fms"%(lat[100],lat[190],lat[198]))
# native direct latency
d=subprocess.Popen(["python3",W+"/catmcp.py","slack"],stdin=subprocess.PIPE,stdout=subprocess.PIPE,text=True)
dl=[]
for k in range(200):
    t=time.perf_counter(); d.stdin.write(json.dumps({"jsonrpc":"2.0","id":k,"method":"tools/call","params":{"name":"slack_post_message","arguments":{"channel_id":"c","text":"hi"}}})+"\n");d.stdin.flush(); d.stdout.readline(); dl.append((time.perf_counter()-t)*1000)
dl.sort(); print("direct latency p50=%.2fms p95=%.2fms"%(dl[100],dl[190]))
# rss
pid=p.pid; print(open(f"/proc/{pid}/status").read().split("VmRSS:")[1].split("\n")[0].strip(), "RSS")
p.kill(); d.kill()
