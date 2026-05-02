use ratatui::layout::{Constraint, Layout, Margin, Rect};
use ratatui::style::{Color, Style, Stylize};
use ratatui::text::{Line, Span};
use ratatui::widgets::{Block, BorderType, Borders, Paragraph, TableState};
use ratatui::Frame;
use std::path::PathBuf;
use std::sync::mpsc;

use crate::api::{ApiClient, PeerInfo, RequestInfo, ServerStatus};
use crate::config::Config;
use crate::event::DataEvent;
use crate::theme::DARK_THEME;
use crate::ui;
use crate::widgets::particles::ParticleSystem;
use crate::widgets::tab_bar::Tab;
use crate::window::WindowState;

pub struct App {
    pub tab: Tab,
    pub should_quit: bool,
    pub show_help: bool,
    pub error_msg: Option<String>,

    pub peers: Vec<PeerInfo>,
    pub peer_state: TableState,
    pub peer_selected: usize,

    pub requests: Vec<RequestInfo>,
    pub request_state: TableState,
    pub request_selected: usize,

    pub status: ServerStatus,
    pub logs: Vec<String>,

    pub tick_count: u64,
    pub last_refresh: i64,

    pub flash: Option<(usize, ui::requests::FlashKind, u64)>,
    pub particles: ParticleSystem,
    pub search_active: bool,
    pub search_query: String,
    pub log_scroll: usize,
    pub window: WindowState,

    pub api: ApiClient,
    #[allow(dead_code)]
    pub config: Config,
    pub audit_log_path: String,
    pub rt: tokio::runtime::Handle,
    pub data_tx: mpsc::Sender<DataEvent>,
}

impl App {
    pub fn new(config: Config, rt: tokio::runtime::Handle, data_tx: mpsc::Sender<DataEvent>) -> Self {
        let api = ApiClient::new(config.api_url.clone(), config.api_key.clone());
        let audit_log = find_audit_log_path();

        Self {
            tab: Tab::Dashboard,
            should_quit: false,
            show_help: false,
            error_msg: None,

            peers: Vec::new(),
            peer_state: TableState::default().with_selected(0),
            peer_selected: 0,

            requests: Vec::new(),
            request_state: TableState::default().with_selected(0),
            request_selected: 0,

            status: ServerStatus {
                daemon: String::new(),
                wireguard: String::new(),
                interface: String::new(),
                listen_port: String::new(),
                peer_online: 0,
                peer_total: 0,
            },
            logs: Vec::new(),

            tick_count: 0,
            last_refresh: 0,

            flash: None,

            particles: ParticleSystem::new(),
            search_active: false,
            search_query: String::new(),
            log_scroll: 0,
            window: WindowState::load(),

            api,
            config,
            audit_log_path: audit_log,
            rt,
            data_tx,
        }
    }

    pub fn on_tick(&mut self) {
        self.tick_count = self.tick_count.wrapping_add(1);

        let now = chrono::Utc::now().timestamp();
        if now - self.last_refresh >= 5 {
            self.last_refresh = now;
            self.refresh_data();
        }

        if let Some((_, _, ref mut count)) = self.flash {
            *count += 1;
            if *count > 30 {
                self.flash = None;
            }
        }
    }

    pub fn apply_data_event(&mut self, event: DataEvent) {
        match event {
            DataEvent::PeersFetched(Ok(resp)) => {
                self.peers = resp.peers;
                if self.peer_selected >= self.peers.len() && !self.peers.is_empty() {
                    self.peer_selected = self.peers.len() - 1;
                    self.peer_state.select(Some(self.peer_selected));
                }
            }
            DataEvent::PeersFetched(Err(e)) => self.error_msg = Some(e),
            DataEvent::RequestsFetched(Ok(resp)) => {
                self.requests = resp.requests;
                if self.request_selected >= self.requests.len() && !self.requests.is_empty() {
                    self.request_selected = self.requests.len() - 1;
                    self.request_state.select(Some(self.request_selected));
                }
            }
            DataEvent::RequestsFetched(Err(e)) => self.error_msg = Some(e),
            DataEvent::StatusFetched(Ok(status)) => self.status = status,
            DataEvent::StatusFetched(Err(e)) => self.error_msg = Some(e),
            DataEvent::RequestApproved(_) | DataEvent::RequestDenied(_) | DataEvent::PeerDeleted(_) => {
                self.refresh_data();
            }
        }
    }

    pub fn refresh_data(&mut self) {
        self.logs = read_audit_log(&self.audit_log_path);
        let api = self.api.clone();
        let tx = self.data_tx.clone();
        let rt = self.rt.clone();

        rt.spawn(async move {
            let peers = api.get_peers().await;
            let _ = tx.send(DataEvent::PeersFetched(peers));
            let reqs = api.get_requests().await;
            let _ = tx.send(DataEvent::RequestsFetched(reqs));
            let status = api.get_status().await;
            let _ = tx.send(DataEvent::StatusFetched(status));
        });
    }

    pub fn approve_request(&mut self, id: &str) {
        let selected = self.request_selected;
        let api = self.api.clone();
        let request_id = id.to_string();
        let rt = self.rt.clone();
        let tx = self.data_tx.clone();
        rt.spawn(async move {
            let result = api.approve_request(&request_id).await;
            let _ = tx.send(DataEvent::RequestApproved(result));
        });
        self.flash = Some((selected, ui::requests::FlashKind::Approve, 0));
    }

    pub fn deny_request(&mut self, id: &str) {
        let selected = self.request_selected;
        let api = self.api.clone();
        let request_id = id.to_string();
        let rt = self.rt.clone();
        let tx = self.data_tx.clone();
        rt.spawn(async move {
            let result = api.deny_request(&request_id).await;
            let _ = tx.send(DataEvent::RequestDenied(result));
        });
        self.flash = Some((selected, ui::requests::FlashKind::Deny, 0));
    }

    pub fn delete_peer(&mut self, name: &str) {
        let api = self.api.clone();
        let peer_name = name.to_string();
        let rt = self.rt.clone();
        let tx = self.data_tx.clone();
        rt.spawn(async move {
            let result = api.delete_peer(&peer_name).await;
            let _ = tx.send(DataEvent::PeerDeleted(result));
        });
    }

    pub fn next_tab(&mut self) {
        self.tab = self.tab.next();
        self.show_help = false;
    }

    pub fn prev_tab(&mut self) {
        self.tab = self.tab.prev();
        self.show_help = false;
    }

    pub fn select_down(&mut self) {
        match self.tab {
            Tab::Peers => {
                if !self.peers.is_empty() {
                    self.peer_selected = (self.peer_selected + 1).min(self.peers.len() - 1);
                    self.peer_state.select(Some(self.peer_selected));
                }
            }
            Tab::Requests => {
                if !self.requests.is_empty() {
                    self.request_selected = (self.request_selected + 1).min(self.requests.len() - 1);
                    self.request_state.select(Some(self.request_selected));
                }
            }
            _ => {}
        }
    }

    pub fn select_up(&mut self) {
        match self.tab {
            Tab::Peers => {
                self.peer_selected = self.peer_selected.saturating_sub(1);
                self.peer_state.select(Some(self.peer_selected));
            }
            Tab::Requests => {
                self.request_selected = self.request_selected.saturating_sub(1);
                self.request_state.select(Some(self.request_selected));
            }
            _ => {}
        }
    }

    pub fn on_shutdown(&mut self) {
        self.window.save();
    }
}

fn fill_area(frame: &mut Frame, area: Rect, bg: Color) {
    if area.width == 0 || area.height == 0 {
        return;
    }
    let line = " ".repeat(area.width as usize);
    for y in 0..area.height {
        frame.buffer_mut().set_string(
            area.x,
            area.y + y,
            &line,
            Style::default().bg(bg),
        );
    }
}

pub fn render(frame: &mut Frame, app: &mut App) {
    let term = frame.area();

    // ═══════════════════════════════════════════════════════
    //  Layer 0: 背景层 (全屏 BG_OUTER + 粒子)
    // ═══════════════════════════════════════════════════════
    fill_area(frame, term, DARK_THEME.bg_outer);

    app.particles.update(term, app.tick_count);
    frame.render_widget(&app.particles, term);

    // ═══════════════════════════════════════════════════════
    //  Layer 1: 窗口层 (BG 填充覆盖粒子 + 双线边框 + 标题栏)
    // ═══════════════════════════════════════════════════════
    let win = app.window.compute(term);
    fill_area(frame, win, DARK_THEME.bg);

    let win_border = Block::default()
        .borders(Borders::ALL)
        .border_type(BorderType::Double)
        .border_style(Style::default().fg(DARK_THEME.border).bg(DARK_THEME.bg));
    frame.render_widget(win_border, win);

    let inner = win.inner(Margin::new(1, 0));
    let title = format!(" WG-TUI · {} ", app.tab.label());
    let title_span = Span::styled(title.clone(), Style::default().fg(DARK_THEME.primary).bold());
    let decor = format!(
        "{}",
        "─".repeat(inner.width.saturating_sub(title.len() as u16) as usize)
    );
    let decor_span = Span::styled(decor, Style::default().fg(DARK_THEME.muted));
    let title_line = Line::from(vec![title_span, decor_span]);
    frame.render_widget(
        Paragraph::new(title_line).style(Style::default().bg(DARK_THEME.bg)),
        Rect::new(win.x + 1, win.y, win.width.saturating_sub(2), 1),
    );

    // ═══════════════════════════════════════════════════════
    //  Window internal layout
    // ═══════════════════════════════════════════════════════
    let chunks = if inner.height >= 6 {
        Layout::vertical([
            Constraint::Length(2),
            Constraint::Min(4),
            Constraint::Length(1),
        ])
        .split(inner)
    } else {
        Layout::vertical([
            Constraint::Length(0),
            Constraint::Min(2),
            Constraint::Length(1),
        ])
        .split(inner)
    };

    // ── Tab bar ──
    fill_area(frame, chunks[0], DARK_THEME.bg);
    crate::widgets::tab_bar::render_tab_bar(frame, chunks[0], app.tab);

    // ═══════════════════════════════════════════════════════
    //  Layer 2+3: 内容区 (BG 填充 → 卡片 → 内容)
    // ═══════════════════════════════════════════════════════
    let content_area = chunks[1];
    fill_area(frame, content_area, DARK_THEME.bg);

    if app.show_help {
        ui::help::render_help(frame, content_area);
    } else {
        match app.tab {
            Tab::Dashboard => render_dashboard(frame, content_area, app),
            Tab::Peers => render_peers(frame, content_area, app),
            Tab::Requests => render_requests(frame, content_area, app),
            Tab::Logs => render_logs(frame, content_area, app),
            Tab::Help => ui::help::render_help(frame, content_area),
        }
    }

    // ── Status bar ──
    render_status_bar(frame, chunks[2], app);
}

fn render_dashboard(frame: &mut Frame, area: Rect, app: &App) {
    fill_area(frame, area, DARK_THEME.bg);

    let chunks = Layout::vertical([
        Constraint::Length(6),
        Constraint::Length(6),
        Constraint::Min(0),
    ])
    .split(area);

    let top = Layout::horizontal([
        Constraint::Ratio(1, 2),
        Constraint::Ratio(1, 2),
    ])
    .split(chunks[0]);

    ui::dashboard::card_server(
        frame, top[0],
        app.status.daemon == "running",
        app.status.wireguard == "ok",
        &app.status.interface,
        &app.status.listen_port,
        app.status.peer_online,
        app.status.peer_total,
    );
    ui::dashboard::card_bindings(frame, top[1]);
    ui::dashboard::card_welcome(frame, chunks[1]);
}

fn render_peers(frame: &mut Frame, area: Rect, app: &mut App) {
    fill_area(frame, area, DARK_THEME.bg);

    let filtered: Vec<&PeerInfo> = if app.search_active && !app.search_query.is_empty() {
        app.peers.iter().filter(|p| {
            p.name.to_lowercase().contains(&app.search_query.to_lowercase())
                || p.address.contains(&app.search_query)
        }).collect()
    } else {
        app.peers.iter().collect()
    };

    use crate::widgets::card::Card;
    if filtered.is_empty() {
        let hint = if app.peers.is_empty() {
            "No peers connected. Navigate to Requests tab to approve join requests."
        } else {
            "No peers match the filter."
        };
        let lines = vec![Line::from(Span::styled(hint, DARK_THEME.muted))];
        Card::new("Peers").render(frame, area, lines);
        return;
    }

    let layout = if app.search_active || !app.search_query.is_empty() {
        Layout::vertical([
            Constraint::Length(1),
            Constraint::Min(4),
            Constraint::Length(6),
        ])
        .split(area)
    } else {
        Layout::vertical([Constraint::Min(4), Constraint::Length(6)]).split(area)
    };

    let list_area = if app.search_active || !app.search_query.is_empty() {
        let hint = if app.search_active {
            format!(" Search: {}▌ (Esc to clear)", app.search_query)
        } else {
            format!(" Filter: {}", app.search_query)
        };
        let search_line = Line::from(Span::styled(
            hint,
            Style::default().fg(DARK_THEME.warning).bg(DARK_THEME.bg),
        ));
        frame.render_widget(Paragraph::new(search_line), layout[0]);
        layout[1]
    } else {
        layout[0]
    };

    let detail_area = layout[if app.search_active || !app.search_query.is_empty() { 2 } else { 1 }];

    let sel = app.peer_selected.min(filtered.len().saturating_sub(1));
    ui::peers::render_peer_list(
        frame, list_area, &filtered,
        &mut app.peer_state,
        sel,
        app.tick_count,
    );

    if let Some(peer) = filtered.get(sel) {
        ui::peers::render_peer_detail(frame, detail_area, peer, app.tick_count);
    }
}

fn render_requests(frame: &mut Frame, area: Rect, app: &mut App) {
    fill_area(frame, area, DARK_THEME.bg);

    use crate::widgets::card::Card;
    if app.requests.is_empty() {
        let lines = vec![Line::from(Span::styled(
            "No pending requests.",
            DARK_THEME.muted,
        ))];
        Card::new("Pending Requests").render(frame, area, lines);
        return;
    }

    let chunks = Layout::vertical([Constraint::Min(4), Constraint::Length(6)]).split(area);

    ui::requests::render_request_list(
        frame, chunks[0], &app.requests,
        &mut app.request_state,
        app.request_selected,
        app.flash,
    );

    if let Some(req) = app.requests.get(app.request_selected) {
        ui::requests::render_request_detail(frame, chunks[1], req);
    }
}

fn render_logs(frame: &mut Frame, area: Rect, app: &App) {
    fill_area(frame, area, DARK_THEME.bg);

    use crate::widgets::card::Card;
    if app.logs.is_empty() {
        let lines = vec![Line::from(Span::styled(
            "No audit log entries. Events will appear here as peers connect or admin actions are taken.",
            DARK_THEME.muted,
        ))];
        Card::new("Audit Log").render(frame, area, lines);
        return;
    }

    ui::logs_view::render_logs(frame, area, &app.logs, app.log_scroll);
}

fn render_status_bar(frame: &mut Frame, area: Rect, app: &App) {
    fill_area(frame, area, DARK_THEME.bg);
    let peer_count = format!("{} peers  {} online", app.peers.len(), app.status.peer_online);
    let req_count = format!("{} pending", app.requests.len());

    let search_hint = if app.search_active {
        "[Esc] exit search  ".to_string()
    } else {
        String::new()
    };

    let tab_hint = match app.tab {
        Tab::Peers => format!("{search_hint}[/] Search  [d] Delete  "),
        Tab::Requests => format!("[a] Approve  [d] Deny  "),
        Tab::Logs => format!("[PgUp/PgDn] Scroll  {}/{}  ", app.log_scroll, app.logs.len()),
        _ => search_hint,
    };

    let full = format!(
        " {} │ {} │ {}[q] Quit  [tab] Switch  [r] Refresh  [?] Help",
        peer_count, req_count, tab_hint,
    );

    let line = Line::from(Span::styled(full, Style::default().fg(DARK_THEME.muted)));
    frame.render_widget(Paragraph::new(line).style(Style::default().bg(DARK_THEME.bg)), area);
}

fn read_audit_log(path: &str) -> Vec<String> {
    if path.is_empty() {
        return Vec::new();
    }
    if let Ok(content) = std::fs::read_to_string(path) {
        let lines: Vec<&str> = content.lines().collect();
        let start = if lines.len() > 50 { lines.len() - 50 } else { 0 };
        lines[start..].iter().map(|l| l.to_string()).collect()
    } else {
        Vec::new()
    }
}

pub fn read_audit_log_file(path: &str) -> Vec<String> {
    read_audit_log(path)
}

fn find_audit_log_path() -> String {
    let paths = [
        "/var/log/wg-mgmt/audit.log".to_string(),
        format!(
            "{}/WG-manager/audit.log",
            std::env::var("HOME").unwrap_or_else(|_| "/tmp".into())
        ),
    ];
    for p in &paths {
        if PathBuf::from(p).exists() {
            return p.clone();
        }
    }
    String::new()
}
