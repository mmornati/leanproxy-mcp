---
name: LeanProxy-MCP
description: X-ray checkpoint. Baggage-scanner pseudocolor on a pale scanner ground, for a local token firewall.
colors:
  scanner-ground: "#f3f5f1"
  scanner-ground-2: "#e7ebe4"
  scanner-paper: "#fbfcfa"
  dense-ink: "#0f1a3a"
  ink-2: "#3a4566"
  ink-3: "#5d6784"
  rule: "#cfd5cc"
  organic-amber: "#f08a24"
  organic-amber-ink: "#a8520a"
  cleared-teal: "#1f9e78"
  cleared-teal-ink: "#0f6e52"
  metal-cobalt: "#2743c9"
  metal-cobalt-ink: "#2139b0"
  threat-red: "#e11d2a"
  threat-red-ink: "#b3121d"
  night-paper: "#0a0f20"
  night-ground: "#0c1226"
  night-ink: "#e9edf6"
typography:
  display:
    fontFamily: "Archivo, ui-sans-serif, system-ui, sans-serif"
    fontSize: "clamp(2.35rem, 4.5vw, 4.25rem)"
    fontWeight: 850
    lineHeight: 1
    letterSpacing: "-0.035em"
  headline:
    fontFamily: "Archivo, ui-sans-serif, system-ui, sans-serif"
    fontSize: "clamp(1.85rem, 3.6vw, 3rem)"
    fontWeight: 800
    lineHeight: 1.04
    letterSpacing: "-0.03em"
  body:
    fontFamily: "Archivo, ui-sans-serif, system-ui, sans-serif"
    fontSize: "1rem"
    fontWeight: 400
    lineHeight: 1.6
  data:
    fontFamily: "JetBrains Mono, ui-monospace, monospace"
    fontSize: "0.88rem"
    fontWeight: 600
    lineHeight: 1.3
rounded:
  sm: "4px"
  md: "8px"
  lg: "10px"
spacing:
  gutter: "clamp(1rem, 4vw, 3rem)"
  section: "clamp(4rem, 9vw, 7.5rem)"
components:
  button-primary:
    backgroundColor: "{colors.dense-ink}"
    textColor: "{colors.scanner-paper}"
    rounded: "{rounded.md}"
    padding: "12px 22px"
    height: "48px"
  button-primary-hover:
    backgroundColor: "{colors.metal-cobalt-ink}"
  button-ghost:
    textColor: "{colors.dense-ink}"
    rounded: "{rounded.md}"
    padding: "12px 22px"
    height: "48px"
  code-slab:
    backgroundColor: "{colors.dense-ink}"
    textColor: "{colors.night-ink}"
    rounded: "{rounded.md}"
    typography: "{typography.data}"
---

# Design System: LeanProxy-MCP

## Overview

**Creative North Star: "The checkpoint scanner."** Every MCP call goes through a baggage X-ray. LeanProxy weighs the cargo (tokens) and boxes the threats (secrets, injected text, drifting tools) in the same pass. The site borrows the scanner's own visual language: see-through pseudocolor shapes on a pale screen, red threat brackets, and readout strips of measured numbers.

The landing page (`overrides/home.html`) is the persuasive surface. Docs pages share the same palette and type, used quietly, for reading.

**Key Characteristics:**

- Pseudocolor as meaning, never decoration: amber = weight (tokens), teal = cleared, cobalt = dense material, red = threat.
- Every number prints its test condition beside it.
- Expanded Archivo display type, like equipment labelling. Mono only for code and measurements.
- Conveyor rails (rows of rollers) divide sections.

## Colors

The palette comes from baggage X-ray pseudocolor. Fills are translucent and multiply where they overlap, the way a scanner renders stacked objects.

### Primary
- **Dense Ink** (`#0f1a3a`): text, header, primary buttons, code slabs. The densest material on the screen.

### Secondary
- **Organic Amber** (`#f08a24`, text `#a8520a`): tokens you pay for. Native-MCP bars, the weight readout, the selection color.
- **Cleared Teal** (`#1f9e78`, text `#0f6e52`): what passed. LeanProxy bars, the scanned side of the scanner, savings figures.
- **Metal Cobalt** (`#2743c9`, text `#2139b0`): links, focus rings, dense objects.

### Tertiary
- **Threat Red** (`#e11d2a`, text `#b3121d`): only for things the scanner caught. Used as corner brackets, never as fills or backgrounds.

### Neutral
- **Scanner Ground** (`#f3f5f1`) and **Scanner Paper** (`#fbfcfa`): the pale screen. Hero and page ground.
- **Rule** (`#cfd5cc`): hairlines inside tables and lists.
- **Night** (`#0a0f20`, `#0c1226`, ink `#e9edf6`): the dark scheme. In dark mode the scanner stage keeps its daylight values, like a lit screen in a dark room.

### Named Rules
- **Red means caught.** Threat red appears only as brackets around something the proxy intercepted or reports as safe (redacted key, "0 of 3" leaks, the headline's "Secrets stay home").
- **Color carries a role.** Never use amber, teal or cobalt for decoration; each maps to weight, cleared or dense.

## Typography

Archivo from Google Fonts, variable width axis (62–125) and weight (100–900). JetBrains Mono for code, readouts and measured values.

### Hierarchy
- **Display** (850, width 112%, clamp 2.35–4.25rem, lh 1, -0.035em): the landing headline only.
- **Headline** (800, width 118%, clamp 1.85–3rem): landing section titles.
- **Docs h1/h2/h3** (800/750/700, width 116/110/104%): docs pages.
- **Body** (400, 1rem landing, 0.8rem docs base, lh 1.6–1.65, max 72ch).
- **Data** (JetBrains Mono 600): readouts, spec values, bar labels. Tabular numerals everywhere.

### Named Rules
- **Mono is for measurement.** Never use monospace as a "technical" costume for prose or labels.

## Layout

- Max width 78rem with a fluid gutter; sections pad `clamp(4rem, 9vw, 7.5rem)`.
- Hero: headline and install block left, lede and actions right, then a full-width scanner band with rails above and below, then a four-cell readout.
- Below 60em everything collapses to one column. Below 36em the scanner crops its viewBox to x 330–1030 so the objects stay legible.
- Lists and tables use hairline rows with a 2px ink top rule (datasheet style), not cards.

## Elevation & Depth

Mostly flat, like a scanner screen. Soft offset shadows only on the primary button and code slabs (`0 1px 2px` + a long, soft drop). No glows, no glass.

## Shapes

Radii of 4px (chips, inline code), 8px (buttons, code) and 10px (terminal). Scanner objects are organic blobs (Catmull-Rom smoothed); the bag is a rounded suitcase outline.

## Components

- **Scanner** (`overrides/partials/xray-scanner.html`, generated from harness numbers): three stacked SVG layers (before, after, labels). The before and after layers are clipped at `--scan`; labels sit unclipped and show only when the line is clear of their object. A transparent `input[type=range]` drives it (drag or arrow keys).
- **Readout strip**: `dl` grid, small sans label over a mono value; the weight value turns teal when fully scanned.
- **Threat bracket** (`.lp-bracket`, `.lp-display__threat`): four gradient corner strokes with `box-decoration-break: slice`, so it hugs wrapped text.
- **Conveyor rail**: repeating 3px rollers every 18px on a 9% ink band; header underline uses the amber/teal/cobalt tri-band.
- **Buttons**: ink primary with an arrow that nudges 3px on hover; ghost with a 1.5px ink outline.
- **Departures board**: docs index rows; an amber band sweeps in from the left on hover.

## Do's and Don'ts

- **Do** print the test condition next to every number.
- **Do** keep threat red for things the proxy caught.
- **Don't** add card grids, eyebrows above headings, gradient text or glows.
- **Don't** claim features the code doesn't deliver (for example, the dashboard's token figures read 0 today).
- **Don't** hand-edit the scanner SVG; regenerate it from the harness numbers.
