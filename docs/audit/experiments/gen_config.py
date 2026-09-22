"""Write $AUDIT_DIR/cat.yaml pointing LeanProxy at one catmcp.py process per catalog server."""
import os, sys
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
from catalog import CAT
W = os.environ.get("AUDIT_DIR", "/tmp/leanproxy-audit")
os.makedirs(W + "/home", exist_ok=True)
lines = ["servers:"]
for s in CAT:
    lines += [f"  - name: {s}", "    transport: stdio", "    stdio:", "      command: python3",
              f"      args: ['{HERE}/catmcp.py','{s}']"]
open(W + "/cat.yaml", "w").write("\n".join(lines) + "\n")
print("wrote", W + "/cat.yaml")
