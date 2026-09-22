import json,subprocess,time,os,threading
W=os.environ.get("AUDIT_DIR","/tmp/leanproxy-audit")
p=subprocess.Popen([os.environ.get("LEANPROXY_BIN","leanproxy-mcp"),"server","run","--stdio","--config",W+"/cat.yaml"],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.DEVNULL,text=True,env=dict(os.environ,HOME=W+"/home"))
def send(m): p.stdin.write(json.dumps(m)+"\n"); p.stdin.flush()
send({"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}); p.stdout.readline()
def call(i,srv="slack",tool="slack_post_message"):
    return {"jsonrpc":"2.0","id":i,"method":"tools/call","params":{"name":"invoke_tool","arguments":{"server":srv,"tool":tool,"arguments":{"channel_id":"c","text":f"msg {i}"}}}}
lat=[]
for k in range(1,61):
    time.sleep(0.12); t=time.perf_counter(); send(call(k)); o=p.stdout.readline(); lat.append((time.perf_counter()-t)*1000)
    assert f"msg {k}" in o, o[:200]
lat.sort(); print("sequential unique-arg invoke p50=%.2fms p95=%.2fms p99=%.2fms"%(lat[30],lat[57],lat[59]))
# pipelined concurrency: 500 requests across 5 servers written at once
N=500; srvs=[("github","create_issue"),("jira","jira_search"),("slack","slack_post_message"),("garmin","get_activity"),("postgres","pg_query")]
t=time.perf_counter()
def writer():
    for k in range(N): s,tl=srvs[k%5]; send(call(1000+k,s,tl))
th=threading.Thread(target=writer); th.start()
got=0; ids=set(); errs=0
while got<N:
    o=p.stdout.readline(); 
    if not o: break
    j=json.loads(o); ids.add(j.get("id")); got+=1; errs+= 'error' in j
el=time.perf_counter()-t; th.join()
print("pipelined %d calls: %.2fs => %.0f req/s, unique ids=%d rate-limit errors=%d"%(N,el,N/el,len(ids),errs))
print(open(f"/proc/{p.pid}/status").read().split("VmRSS:")[1].split("\n")[0].strip(),"RSS", "threads:",open(f"/proc/{p.pid}/status").read().split("Threads:")[1].split("\n")[0].strip())
p.kill()
