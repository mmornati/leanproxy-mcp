import os
import sys,math,re,json
sys.path.insert(0,os.path.dirname(os.path.abspath(__file__))); from catalog import CAT
Q=[("open a bug ticket in the repo about the login crash","github","create_issue"),
("show me the CI logs of the failed job","github","get_job_logs"),
("merge PR 42 with squash","github","merge_pull_request"),
("what changed in pull request 17, list the files","github","get_pull_request_files"),
("read the README file from the main branch","github","get_file_contents"),
("find code that calls parseConfig across repos","github","search_code"),
("are there leaked credentials detected in my repo","github","list_secret_scanning_alerts"),
("start a new feature branch","github","create_branch"),
("approve the pull request","github","create_pull_request_review"),
("kick off the deploy workflow manually","github","run_workflow"),
("vulnerable dependencies in the project","github","list_dependabot_alerts"),
("what is the latest release version","github","get_latest_release"),
("find jira tickets assigned to me that are in progress","jira","jira_search"),
("move PROJ-12 to done","jira","jira_transition_issue"),
("log 2 hours on PROJ-7","jira","jira_add_worklog"),
("which sprints are active on board 5","jira","jira_get_sprints_from_board"),
("mark PROJ-3 as blocking PROJ-4","jira","jira_link_issues"),
("create a story in project ABC","jira","jira_create_issue"),
("find the onboarding page in confluence","jira","confluence_search"),
("close the current sprint","jira","jira_update_sprint"),
("send a message to the team channel","slack","slack_post_message"),
("reply in the thread","slack","slack_reply_to_thread"),
("react with thumbs up to that message","slack","slack_add_reaction"),
("what did people say in #general today","slack","slack_get_channel_history"),
("set my status to on vacation","slack","slack_set_status"),
("post the report in slack tomorrow at 9am","slack","slack_schedule_message"),
("search slack for the outage discussion","slack","slack_search_messages"),
("how did I sleep last night","garmin","get_sleep_data"),
("my last run pace and heart rate","garmin","get_activity"),
("am I ready to train hard today","garmin","get_training_readiness"),
("what is my predicted marathon time","garmin","get_race_predictions"),
("record my weight 72kg","garmin","add_weigh_in"),
("how many kilometers on my running shoes","garmin","get_gear"),
("show HRV trend","garmin","get_hrv_data"),
("schedule the interval workout for Friday","garmin","schedule_workout"),
("my VO2 max","garmin","get_max_metrics"),
("how many steps today","garmin","get_steps_data"),
("blood oxygen overnight","garmin","get_spo2_data"),
("what columns does the users table have","postgres","pg_describe_table"),
("why is this query slow, show the plan","postgres","pg_explain_query"),
("count orders placed yesterday","postgres","pg_query"),
("delete the test rows from the sessions table","postgres","pg_execute"),
("what queries are running right now","postgres","pg_active_queries"),
("which tables are biggest on disk","postgres","pg_table_size"),
]
docs=[]
for s,tools in CAT.items():
    for n,d,sch in tools: docs.append((s,n,d,sch))
STOP=set("a an the in of to my me my for and or is are with that this what which how i on at by from be do did show get list".split())
def stem(w):
    for suf in ("ing","ies","es","s","ed"):
        if len(w)>4 and w.endswith(suf): return w[:-len(suf)]+("y" if suf=="ies" else "")
    return w
def toks(t):
    t=re.sub(r"([a-z])([A-Z])",r"\1 \2",t)
    return [stem(w) for w in re.findall(r"[a-z0-9]+",t.lower().replace("_"," ")) if w not in STOP]
SYN={"pr":["pull","request"],"ticket":["issue"],"bug":["issue"],"ci":["workflow","action"],"repo":["repository"],"message":["message"],"sleep":["sleep"],"weight":["weigh"],"shoe":["gear"],"plan":["explain"],"column":["describe","table"]}
def expand(ts):
    out=list(ts)
    for t in ts: out+= [stem(x) for x in SYN.get(t,[])]
    return out
D=[toks(f"{s} {n} {n} {d} "+" ".join(sch["properties"].keys())) for s,n,d,sch in docs]
N=len(D); avg=sum(map(len,D))/N
df={}
for d in D:
    for w in set(d): df[w]=df.get(w,0)+1
def bm25(q,k1=1.2,b=0.75,syn=False):
    qt=toks(q); qt=expand(qt) if syn else qt
    sc=[]
    for i,d in enumerate(D):
        s=0
        for w in qt:
            f=d.count(w)
            if f: s+=math.log(1+(N-df[w]+.5)/(df[w]+.5))*f*(k1+1)/(f+k1*(1-b+b*len(d)/avg))
        sc.append((s,i))
    sc.sort(reverse=True); return [i for s,i in sc if s>0]
def substr(q):   # replicates pkg/mcp matchesQuery: every query word must be a substring
    words=q.lower().split(); res=[]
    for i,(s,n,d,_) in enumerate(docs):
        line=f"{s}_{n}: {d.lower()}"
        if all(w in line for w in words): res.append(i)
    return res
def fmt(i):  # same compact format as list_tools output
    s,n,d,sch=docs[i]; return f"{s}_{n}: {d} [{', '.join(sch['required'])}] {{{', '.join(k for k in sch['properties'] if k not in sch['required'])}}}"
def evaluate(fn,k=5):
    r1=r5=0; toks_=0; empty=0
    for q,s,n in Q:
        res=fn(q); ids=[(docs[i][0],docs[i][1]) for i in res]
        if not res: empty+=1
        r1+= ids[:1]==[(s,n)]; r5+= (s,n) in ids[:k]
        toks_+= sum(len(fmt(i)) for i in res[:k])//4
    n=len(Q); return r1/n,r5/n,toks_/n,empty
print("queries:",len(Q),"catalog tools:",len(docs))
for name,fn in [("substring-AND (current matchesQuery)",substr),("BM25",bm25),("BM25+synonyms",lambda q:bm25(q,syn=True))]:
    a,b,c,e=evaluate(fn); print(f"{name:40s} recall@1={a:.0%} recall@5={b:.0%} avg_tokens(top5)={c:.0f} empty={e}")
# current router flow cost: must pick the right server then dump its full tool list
ltok={"github":1637,"jira":772,"slack":441,"garmin":760,"postgres":281}
print("current flow list_tools(server) avg tokens per query: %.0f"%(sum(ltok[s] for _,s,_ in Q)/len(Q)))
