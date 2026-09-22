import json,subprocess,os,sys
W=os.environ.get("AUDIT_DIR","/tmp/leanproxy-audit")
mode=sys.argv[1:]
p=subprocess.Popen([os.environ.get("LEANPROXY_BIN","leanproxy-mcp")]+mode,stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.DEVNULL,text=True,env=dict(os.environ,HOME=W+"/home"))
def rpc(i,m,params):
    p.stdin.write(json.dumps({"jsonrpc":"2.0","id":i,"method":m,"params":params})+"\n");p.stdin.flush();return p.stdout.readline()
print(rpc(1,"initialize",{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"t","version":"1"}})[:120])
print(rpc(2,"tools/list",{})[:300])
print(rpc(3,"tools/call",{"name":"invoke_tool","arguments":{"server":"slack","tool":"slack_post_message","arguments":{"channel_id":"c","text":"x"}}}))
print(rpc(4,"tools/call",{"name":"slack.slack_post_message","arguments":{"channel_id":"c","text":"x"}}))
p.kill()
