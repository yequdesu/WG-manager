# TUI-Template Architecture

Ratatui-based terminal UI framework. This document explains the architecture so an AI agent can use this template to build a TUI application.

---

## 1. Rendering Layers (L0 → L1 → L2 → L3)

Every frame is rendered in strict z-order. Upper layers physically overwrite lower layers.

| Layer | Color | Content |
|-------|-------|---------|
| **L0** | `bg_outer` (#080B12) | Full-terminal background color + background effect (particles or custom) |
| **L1** | `bg` (#0D1117) | Window area fill + double border + title bar |
| **L2** | `bg` (#0D1117) | Content panel area fill, gaps between cards visible |
| **L3** | `surface` (#161B22) | Card interiors + text/data content |

**Critical**: `fill_area()` must be called at each layer transition. It writes space characters with the target background color, physically erasing any glyphs from the layer beneath. Without it, background effect characters (particles) will bleed through.

```rust
fill_area(frame, area, DARK_THEME.bg);  // Clear area with spaces + bg
```

---

## 2. Event Loop

```
┌─────────────────────────────────────────────────────┐
│  main()                                            │
│  ┌──────┐  ┌──────┐  ┌──────────┐  ┌───────────┐ │
│  │tokio │  │config│  │Terminal  │  │Panic      │ │
│  │runtime│ │loader│  │Setup     │  │Handler    │ │
│  └──┬───┘  └──┬───┘  └────┬─────┘  └─────┬─────┘ │
│     │         │           │              │       │
│     ▼         ▼           ▼              ▼       │
│  ┌──────────────────────────────────────────┐    │
│  │          run() event loop                 │    │
│  │                                          │    │
│  │  while !should_quit:                     │    │
│  │    1. Drain mpsc data channel            │    │
│  │    2. terminal.draw(render)              │    │
│  │    3. handler.next() → Event             │    │
│  │       ├─ Key → keyboard handler          │    │
│  │       ├─ Tick(50ms) → app.on_tick()      │    │
│  │       └─ Mouse → scroll handler          │    │
│  └──────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────┘
```

**Data flow**: Background tokio tasks send API responses through `mpsc::Sender<DataEvent>` → main loop drains with `handler.try_recv_data()` → `app.apply_data_event()` updates state.

---

## 3. Component System

### Card (`widgets/card.rs`)

Bordered container with title. Two rendering modes:

```rust
// Mode 1: Full render (border + content)
Card::new("Server").render(frame, area, vec![
    Line::from(Span::styled("content", style)),
]);

// Mode 2: Border only, returns inner Rect for custom content
let inner = Card::new("Peers").render_block(frame, area);
// ... render table/widget into inner
```

### Tab Bar (`widgets/tab_bar.rs`)

```rust
// Define your tabs (REPLACE these)
pub enum Tab { Tab1, Tab2, Tab3, Tab4 }

// Render the tab bar
render_tab_bar(frame, area, active_tab);
```

Each tab's label is defined in `Tab::label()`. The active tab gets `primary` color highlight.

### Table (`widgets/table.rs`)

Generic table with highlight support:

```rust
use widgets::table::{render_simple_table, cell_span, truncate};

render_simple_table(
    frame, area,
    &["Name", "IP", "Status"],           // headers
    &vec![vec!["item1".into(), "10.0.0.1".into(), "online".into()]], // rows
    &mut state,
    0,                                     // highlight row index
);
```

### Status Dot (`widgets/status_dot.rs`)

Breathing animation dot for online/offline indicators:

```rust
status_dot(frame, area, online, tick_count);
// Renders ● (online, pulsing green) or ○ (offline, muted gray)
```

---

## 4. Background Effects

The `background.rs` module defines a trait:

```rust
pub trait BackgroundEffect: Widget {
    fn update(&mut self, area: Rect, window: Rect, tick: u64);
}
```

The default implementation is `NoopBackground` (blank background). To add an effect:

1. Implement `BackgroundEffect` + `Widget` for your type
2. Replace `Box::new(NoopBackground)` in `app.rs` with your implementation

See `examples/particles.rs` for a physics-based particle system reference.

---

## 5. Window Management

`window.rs` provides:
- **Position**: `Ctrl+Arrows` moves the window
- **Resize**: `=` / `-` zoom in/out (50%-100% of terminal)
- **Reset**: `0` resets to centered default
- **Persistence**: Window position/size saved to `~/.config/your-app/window-state.json`

These keys are handled in `main.rs` (Ctrl+Arrows) and app.rs keyboard handler (=/-/0 keys). Window state is loaded in `App::new()` via `WindowState::load()` and saved in `app.on_shutdown()` via `self.window.save()`.

---

## 6. How to Use This Template (Agent Guide)

### Step 1: Define your data

In `app.rs`:
- Replace `data_placeholder` with your actual data fields
- Add `TableState` for each list/table
- Define a `DataEvent` enum in `event.rs` for your API response types

### Step 2: Define your API client

In `api.rs`:
- Add your API response structs (with `#[derive(Deserialize)]`)
- Add methods to `ApiClient` for each endpoint (follow the template)

### Step 3: Define your tabs

In `widgets/tab_bar.rs`:
- Replace `Tab` enum variants (Tab1..4) with your tab names
- Update `label()` to return your tab labels

### Step 4: Replace tab renderers

In `app.rs`:
- Replace `render_tab1()` through `render_tab4()` with your content
- Use `Card` for containers, `render_simple_table` for data tables
- Always call `fill_area()` before rendering content in a new area

### Step 5: Wire up keyboard

In `main.rs`:
- Add your action keys to the `match key.code { ... }` block
- Wire them to App methods you define

### Step 6: Custom background (optional)

Copy `examples/particles.rs` or write your own `BackgroundEffect` impl. Replace the `Box::new(NoopBackground)` in `App::new()`.

---

## 7. File Reference

| File | Role | Agent Action |
|------|------|-------------|
| `src/main.rs` | Entry point, event loop, keyboard | REPLACE: key mappings, app init |
| `src/app.rs` | App state, render, tabs | REPLACE: data fields, tab renderers |
| `src/config.rs` | Config file loader | REPLACE: config keys |
| `src/api.rs` | HTTP client | REPLACE: API types + methods |
| `src/theme.rs` | Color system | KEEP (tweak colors if desired) |
| `src/event.rs` | Event system | KEEP |
| `src/window.rs` | Window position/resize | KEEP |
| `src/background.rs` | Background effect trait | KEEP trait, REPLACE impl |
| `src/widgets/card.rs` | Card container | KEEP |
| `src/widgets/tab_bar.rs` | Tab bar | REPLACE: tab names |
| `src/widgets/table.rs` | Generic table | KEEP |
| `src/widgets/status_dot.rs` | Status indicator | KEEP |
| `examples/particles.rs` | Particle background | Reference only |
| `build-linux.sh` | Cross-compile script | KEEP |

---

## 8. Build & Run

```bash
# Native build
cargo build --release
./target/release/tui-template

# Cross-compile for Linux from any platform
bash build-linux.sh
scp tui-template-linux user@server:~/.local/bin/
```
