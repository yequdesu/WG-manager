// ============================================================
// AGENT: REPLACE — This is the central App state and renderer.
//
// KEY PATTERNS TO KEEP:
// 1. App struct holds ALL state (tabs, data, window, search, confirm)
// 2. on_tick() runs every 50ms: tick counter + timed refresh
// 3. apply_data_event() receives async API responses via mpsc channel
// 4. render() implements layered rendering (L0→L1→L2→L3)
// 5. fill_area() physically clears characters to prevent particle bleed
//
// WHAT TO REPLACE:
// - Your domain-specific data fields (peers, requests, etc.)
// - Tab content rendering (render_dashboard, render_peers, etc.)
// - API data types in apply_data_event()
// - Status bar content
// ============================================================

use ratatui::layout::{Constraint, Layout, Margin, Rect};
use ratatui::style::{Color, Style, Stylize};
use ratatui::text::{Line, Span};
use ratatui::widgets::{Block, BorderType, Borders, Paragraph};
use ratatui::Frame;
use std::sync::mpsc;

use crate::background::{BackgroundEffect, NoopBackground};
use crate::config::Config;
use crate::event::DataEvent;
use crate::theme::DARK_THEME;
use crate::window::WindowState;
use crate::widgets::tab_bar::Tab;

// ============================================================
// AGENT: REPLACE — Add your domain data types here.
// ============================================================

pub struct App {
    pub tab: Tab,
    pub should_quit: bool,
    pub show_help: bool,
    pub error_msg: Option<String>,
    pub tick_count: u64,
    pub last_refresh: i64,

    // AGENT: REPLACE — Replace with your own data fields
    // Example: pub items: Vec<YourItem>,
    //          pub item_state: TableState,
    //          pub item_selected: usize,

    /// Domain data: store whatever your TUI displays
    pub data_placeholder: Vec<String>,

    /// Search / filter
    pub search_active: bool,
    pub search_query: String,
    pub log_scroll: usize,

    /// Delete confirmation (two-step: first 'd', then 'd'/'y')
    pub confirm_delete: bool,
    pub confirm_timer: u16,

    /// Window management
    pub window: WindowState,

    /// Background animation (pluggable)
    // AGENT: REPLACE — Swap NoopBackground with your own BackgroundEffect impl
    pub background: Box<dyn BackgroundEffect>,

    /// Infrastructure
    pub api: ApiClient,
    pub config: Config,
    pub rt: tokio::runtime::Handle,
    pub data_tx: mpsc::Sender<DataEvent>,
}

// ============================================================
// AGENT: REPLACE — Define your API client
// ============================================================
// Minimal stub — replace with your actual HTTP client.
use crate::api::ApiClient;

impl App {
    pub fn new(config: Config, rt: tokio::runtime::Handle, data_tx: mpsc::Sender<DataEvent>) -> Self {
        let api = ApiClient::new(config.api_url.clone(), config.api_key.clone());

        Self {
            tab: Tab::Tab1,
            should_quit: false,
            show_help: false,
            error_msg: None,
            tick_count: 0,
            last_refresh: 0,

            // AGENT: REPLACE — Initialize your data fields
            data_placeholder: Vec::new(),

            search_active: false,
            search_query: String::new(),
            log_scroll: 0,

            confirm_delete: false,
            confirm_timer: 0,

            window: WindowState::load(),

            // AGENT: REPLACE — Replace with your background implementation
            background: Box::new(NoopBackground),

            api,
            config,
            rt,
            data_tx,
        }
    }

    // ============================================================
    // AGENT: KEEP — Core event methods (do not change signatures)
    // ============================================================

    pub fn on_tick(&mut self) {
        self.tick_count = self.tick_count.wrapping_add(1);

        let now = chrono::Utc::now().timestamp();
        if now - self.last_refresh >= 5 {
            self.last_refresh = now;
            self.refresh_data();
        }

        if self.confirm_delete {
            self.confirm_timer += 1;
            if self.confirm_timer > 60 {
                self.confirm_delete = false;
                self.confirm_timer = 0;
            }
        }
    }

    pub fn refresh_data(&mut self) {
        let api = self.api.clone();
        let tx = self.data_tx.clone();
        let rt = self.rt.clone();

        // AGENT: REPLACE — Call your API endpoints here
        rt.spawn(async move {
            let result = api.health_check().await;
            let _ = tx.send(DataEvent::HealthCheck(result));
        });
    }

    // AGENT: REPLACE — Handle incoming async data
    pub fn apply_data_event(&mut self, event: DataEvent) {
        match event {
            DataEvent::HealthCheck(Ok(_)) => {
                // AGENT: ADD — Update your state with fetched data
            }
            DataEvent::HealthCheck(Err(e)) => {
                self.error_msg = Some(e);
            }
        }
    }

    // ============================================================
    // AGENT: KEEP — Navigation methods
    // ============================================================

    pub fn next_tab(&mut self) {
        self.tab = self.tab.next();
        self.show_help = false;
    }

    pub fn prev_tab(&mut self) {
        self.tab = self.tab.prev();
        self.show_help = false;
    }

    pub fn select_down(&mut self) {
        // AGENT: ADD — Navigation logic for your data lists
    }

    pub fn select_up(&mut self) {
        // AGENT: ADD — Navigation logic for your data lists
    }

    pub fn on_shutdown(&mut self) {
        self.window.save();
    }
}

// ============================================================
// AGENT: KEEP — Layered rendering protocol (L0→L1→L2→L3)
//
// L0: Full-screen background color + background effect (particles, etc.)
// L1: Window background fill + double border + title bar
// L2: Content panels filled with bg
// L3: Cards (surface) + text content
//
// Modify render_dashboard/render_peers/etc. for your content.
// Keep: fill_area(), window layout, tab bar, status bar.
// ============================================================

pub fn render(frame: &mut Frame, app: &mut App) {
    let term = frame.area();

    // ═══════════════════════════════════════════════════════
    //  Layer 0: Background
    // ═══════════════════════════════════════════════════════
    fill_area(frame, term, DARK_THEME.bg_outer);
    let win = app.window.compute(term);
    app.background.update(term, win, app.tick_count);
    app.background.render(term, frame.buffer_mut());

    // ═══════════════════════════════════════════════════════
    //  Layer 1: Window
    // ═══════════════════════════════════════════════════════
    fill_area(frame, win, DARK_THEME.bg);
    let win_border = Block::default()
        .borders(Borders::ALL)
        .border_type(BorderType::Double)
        .border_style(Style::default().fg(DARK_THEME.border).bg(DARK_THEME.bg));
    frame.render_widget(win_border, win);

    let inner = win.inner(Margin::new(1, 0));
    // AGENT: REPLACE — Window title text
    let title = format!(" TUI-Template · {} ", app.tab.label());
    let title_span = Span::styled(title.clone(), Style::default().fg(DARK_THEME.primary).bold());
    let decor = "─".repeat(inner.width.saturating_sub(title.len() as u16) as usize);
    let title_line = Line::from(vec![title_span, Span::styled(decor, Style::default().fg(DARK_THEME.muted))]);
    frame.render_widget(
        Paragraph::new(title_line).style(Style::default().bg(DARK_THEME.bg)),
        Rect::new(win.x + 1, win.y, win.width.saturating_sub(2), 1),
    );

    // ═══════════════════════════════════════════════════════
    //  Window internal layout
    // ═══════════════════════════════════════════════════════
    let chunks = if inner.height >= 6 {
        Layout::vertical([Constraint::Length(2), Constraint::Min(4), Constraint::Length(1)]).split(inner)
    } else {
        Layout::vertical([Constraint::Length(0), Constraint::Min(2), Constraint::Length(1)]).split(inner)
    };

    // Tab bar
    fill_area(frame, chunks[0], DARK_THEME.bg);
    crate::widgets::tab_bar::render_tab_bar(frame, chunks[0], app.tab);

    // Content
    let content_area = chunks[1];
    fill_area(frame, content_area, DARK_THEME.bg);

    if app.show_help {
        render_help(frame, content_area);
    } else {
        // ============================================================
        // AGENT: REPLACE — Your tab content rendering.
        // Each match arm should render one tab's content.
        // Use Card::new("Title").render(frame, area, lines) for cards.
        // Use render_simple_table() from widgets::table for data tables.
        // ============================================================
        match app.tab {
            Tab::Tab1 => render_tab1(frame, content_area, app),
            Tab::Tab2 => render_tab2(frame, content_area, app),
            Tab::Tab3 => render_tab3(frame, content_area, app),
            Tab::Tab4 => render_tab4(frame, content_area, app),
        }
    }

    // Status bar
    render_status_bar(frame, chunks[2], app);
}

// ============================================================
// AGENT: REPLACE — Your tab content renderers.
// Replace these with your own tab views.
// ============================================================

fn render_tab1(frame: &mut Frame, area: Rect, _app: &App) {
    use crate::widgets::card::Card;
    Card::new("Tab 1").render(frame, area, vec![
        Line::from(Span::styled(
            "  AGENT: Replace with your Dashboard / Overview content",
            DARK_THEME.muted,
        )),
    ]);
}

fn render_tab2(frame: &mut Frame, area: Rect, _app: &App) {
    use crate::widgets::card::Card;
    Card::new("Tab 2").render(frame, area, vec![
        Line::from(Span::styled(
            "  AGENT: Replace with your data list / table content",
            DARK_THEME.muted,
        )),
    ]);
}

fn render_tab3(frame: &mut Frame, area: Rect, _app: &App) {
    use crate::widgets::card::Card;
    Card::new("Tab 3").render(frame, area, vec![
        Line::from(Span::styled(
            "  AGENT: Replace with your form / action content",
            DARK_THEME.muted,
        )),
    ]);
}

fn render_tab4(frame: &mut Frame, area: Rect, _app: &App) {
    use crate::widgets::card::Card;
    Card::new("Tab 4").render(frame, area, vec![
        Line::from(Span::styled(
            "  AGENT: Replace with your log / history content",
            DARK_THEME.muted,
        )),
    ]);
}

fn render_help(frame: &mut Frame, area: Rect) {
    use ratatui::style::Stylize;
    use ratatui::widgets::Block;

    let block = Block::default()
        .borders(Borders::ALL)
        .border_style(Style::default().fg(DARK_THEME.primary).bg(DARK_THEME.bg))
        .style(Style::default().bg(DARK_THEME.bg))
        .title(Span::styled(" HELP ", Style::default().fg(DARK_THEME.primary).bold()));

    let inner = block.inner(area);
    frame.render_widget(block, area);

    let lines = vec![
        Line::from(""),
        Line::from(vec![Span::styled("  Tab / ←→     ", DARK_THEME.primary), Span::styled("Switch tabs", DARK_THEME.text)]),
        Line::from(vec![Span::styled("  j / k / ↑↓   ", DARK_THEME.primary), Span::styled("Navigate lists", DARK_THEME.text)]),
        Line::from(vec![Span::styled("  /             ", DARK_THEME.primary), Span::styled("Search / filter", DARK_THEME.text)]),
        Line::from(vec![Span::styled("  Ctrl+Arrows   ", DARK_THEME.primary), Span::styled("Move window", DARK_THEME.text)]),
        Line::from(vec![Span::styled("  = / - / 0     ", DARK_THEME.primary), Span::styled("Zoom in / out / reset", DARK_THEME.text)]),
        Line::from(vec![Span::styled("  r / q / ?     ", DARK_THEME.primary), Span::styled("Refresh / Quit / Help", DARK_THEME.text)]),
        Line::from(""),
        Line::from(Span::styled("  TUI-Template  ·  Ratatui Framework", DARK_THEME.muted)),
    ];

    frame.render_widget(Paragraph::new(lines).style(Style::default().fg(DARK_THEME.text)), inner);
}

fn render_status_bar(frame: &mut Frame, area: Rect, app: &App) {
    fill_area(frame, area, DARK_THEME.bg);

    if app.confirm_delete {
        let msg = Line::from(Span::styled(
            " Confirm delete? [d] confirm  [any other key] cancel",
            Style::default().fg(DARK_THEME.danger).bg(DARK_THEME.bg),
        ));
        frame.render_widget(Paragraph::new(msg), area);
        return;
    }

    // AGENT: REPLACE — Your status bar content
    let full = format!(" [q] Quit  [tab] Switch  [r] Refresh  [?] Help");
    let line = Line::from(Span::styled(full, Style::default().fg(DARK_THEME.muted)));
    frame.render_widget(Paragraph::new(line).style(Style::default().bg(DARK_THEME.bg)), area);
}

// ============================================================
// AGENT: KEEP — fill_area(): physically clear an area to
// prevent background effect characters from bleeding through.
// Always call this before rendering content in a new layer.
// ============================================================

fn fill_area(frame: &mut Frame, area: Rect, bg: Color) {
    if area.width == 0 || area.height == 0 { return; }
    let line = " ".repeat(area.width as usize);
    for y in 0..area.height {
        frame.buffer_mut().set_string(area.x, area.y + y, &line, Style::default().bg(bg));
    }
}
