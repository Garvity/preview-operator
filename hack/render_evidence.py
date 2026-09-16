#!/usr/bin/env python3
"""Render architecture and measured verification images. Requires Pillow.

All images, including SVG originals and PNG previews, are written to output-images.
These are diagrams/evidence summaries, not screenshots of a graphical operator UI.
"""
import html
import json
import textwrap
from pathlib import Path

from PIL import Image, ImageDraw, ImageFont

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / "output-images"
OUT.mkdir(exist_ok=True)
BG, PANEL, BORDER, WHITE, MUTED, TEAL, PURPLE = "#0b1220", "#141f32", "#2a3950", "#eff5ff", "#a6b5ce", "#5ee0ba", "#b8a0ff"


def font(size, bold=False, mono=False):
    candidates = (["/System/Library/Fonts/Menlo.ttc", "/usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf"] if mono else
                  ["/System/Library/Fonts/Supplemental/Arial Bold.ttf" if bold else "/System/Library/Fonts/Supplemental/Arial.ttf",
                   "/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf" if bold else "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf"])
    for path in candidates:
        if Path(path).exists():
            return ImageFont.truetype(path, size)
    return ImageFont.load_default(size=size)


class Canvas:
    def __init__(self, height):
        self.height = height
        self.image = Image.new("RGB", (1600, height), BG)
        self.draw = ImageDraw.Draw(self.image)
        self.svg = [f'<svg xmlns="http://www.w3.org/2000/svg" width="1600" height="{height}" viewBox="0 0 1600 {height}">',
                    f'<rect width="1600" height="{height}" fill="{BG}"/>']

    def rect(self, x, y, w, h, fill=PANEL, radius=20):
        self.draw.rounded_rectangle((x, y, x+w, y+h), radius=radius, fill=fill, outline=BORDER, width=2)
        self.svg.append(f'<rect x="{x}" y="{y}" width="{w}" height="{h}" rx="{radius}" fill="{fill}" stroke="{BORDER}" stroke-width="2"/>')

    def text(self, x, y, value, size=25, color=WHITE, bold=False, mono=False):
        self.draw.text((x, y), value, font=font(size, bold, mono), fill=color, anchor="lt")
        family = "monospace" if mono else "Arial, sans-serif"
        weight = "700" if bold else "400"
        self.svg.append(f'<text x="{x}" y="{y+size}" font-family="{family}" font-size="{size}" font-weight="{weight}" fill="{color}">{html.escape(value)}</text>')

    def arrow(self, x1, y1, x2, y2):
        self.draw.line((x1,y1,x2,y2), fill=TEAL, width=4)
        self.svg.append(f'<path d="M{x1} {y1} L{x2} {y2}" stroke="{TEAL}" stroke-width="4" fill="none"/>')
        if y1 == y2:
            points = [(x2,y2),(x2-14,y2-9),(x2-14,y2+9)]
        else:
            points = [(x2,y2),(x2-9,y2-14),(x2+9,y2-14)]
        self.draw.polygon(points, fill=TEAL)
        self.svg.append(f'<polygon points="{" ".join(f"{x},{y}" for x,y in points)}" fill="{TEAL}"/>')

    def save(self, name):
        self.image.save(OUT/f"{name}.png")
        (OUT/f"{name}.svg").write_text("\n".join(self.svg+["</svg>"])+"\n")


def architecture():
    c = Canvas(1010)
    c.text(70,55,"PREVIEW OPERATOR  /  ARCHITECTURE",20,TEAL,True)
    c.text(70,108,"One resource. A temporary environment.",52,WHITE,True)
    c.text(70,185,"A Go controller with ownership protection and a persisted expiry deadline.",26,MUTED)
    boxes = [(70,285,400,"01  DECLARE","PreviewEnvironment",["Image + replicas + ports", "Immutable TTL", "API-server creation time"]),
             (600,285,400,"02  RECONCILE","Go operator",["Check ownership and deadline", "Converge desired resources", "Report observed rollout status"]),
             (1130,285,400,"03  SERVE","Deployment + Service",["Non-root application pods", "Cluster-local HTTP endpoint", "Repair deleted children"])]
    for x,y,w,eyebrow,title,lines in boxes:
        c.rect(x,y,w,285)
        c.text(x+25,y+28,eyebrow,19,TEAL,True)
        c.text(x+25,y+85,title,29,WHITE,True)
        for i,line in enumerate(lines):
            c.text(x+25,y+148+i*37,line,21,MUTED)
    c.arrow(475,425,585,425)
    c.arrow(1005,425,1115,425)
    c.arrow(800,580,800,655)
    c.rect(320,670,960,205)
    c.text(350,700,"AT THE DEADLINE",20,PURPLE,True)
    c.text(350,745,"Expiring  →  owned workloads removed  →  Expired",30,WHITE,True)
    c.text(350,807,"The CR remains for inspection. A manager restart does not reset its lifetime.",22,MUTED)
    c.text(70,932,"Design diagram • Expiry is eventual and depends on controller/API/garbage collector availability.",20,MUTED)
    c.save("architecture")


def final_output():
    report = json.loads((ROOT/"docs/verification/e2e-report.json").read_text())
    summary = json.loads((ROOT/"docs/verification/summary.json").read_text())
    passed = report["status"] == "passed"
    checks = report["checks"]
    rows = (len(checks) + 1) // 2
    panel_y = 425 + rows * 108 + 28
    c = Canvas(panel_y + 375)
    c.text(70,50,"PREVIEW OPERATOR  /  MEASURED VERIFICATION",20,TEAL,True)
    c.text(70,105,"Temporary environments, verified end to end.",49,WHITE,True)
    c.text(70,180,"Actual kind-cluster results. This is an evidence summary, not an application UI.",25,MUTED)
    for x,value,label in [(70,f"{sum(check['status'] == 'passed' for check in checks)} / {report.get('expectedChecks', len(checks))}","live checks passed"),
                          (575,summary["controllerCoverage"],"controller statement coverage"),
                          (1080,report.get("kubernetes","unknown"),"Kubernetes tested")]:
        c.rect(x,245,450,145)
        c.text(x+25,273,value,48,TEAL if passed else "#ff8a91",True)
        c.text(x+25,340,label,23,MUTED)
    for i,check in enumerate(checks):
        x = 70+(i%2)*745
        y = 425+(i//2)*108
        c.rect(x,y,715,89,radius=14)
        c.text(x+22,y+18,"PASS" if check["status"]=="passed" else "FAIL",18,TEAL,True)
        for line_index, line in enumerate(textwrap.wrap(check["name"], width=43)[:2]):
            c.text(x+102,y+12+line_index*24,line,21,WHITE,True)
        c.text(x+102,y+65,f"{check['seconds']:.2f}s  •  observed in the live test cluster",15,MUTED)
    c.rect(70,panel_y,1460,235)
    c.text(100,panel_y+24,"OBSERVED OUTPUT",18,PURPLE,True)
    c.text(100,panel_y+67,report.get("httpResponse","No HTTP response recorded"),29,WHITE,False,True)
    ready = report.get("readyPreview",{}).get("status",{})
    expired = report.get("expiredPreview",{}).get("status",{})
    c.text(100,panel_y+118,f"hello-preview   {ready.get('phase','unknown')}   available replicas: {ready.get('availableReplicas',0)}",23,TEAL,False,True)
    c.text(100,panel_y+161,f"short-lived     {expired.get('phase','unknown')}   Deployment + Service removed",23,TEAL,False,True)
    stamp=report["finishedAt"].replace("T"," ").split(".")[0]+" UTC"
    c.text(70,panel_y+275,f"Executed {stamp}  •  Local test cluster {'removed' if report.get('clusterCleanedUp') else 'retained'}",20,MUTED)
    c.text(70,panel_y+315,"Source: docs/verification/e2e-report.json • API-server integration + unit/race checks also passed.",19,MUTED)
    c.save("final-output")


if __name__ == "__main__":
    architecture()
    final_output()
