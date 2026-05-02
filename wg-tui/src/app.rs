use ratatui::layout::{Constraint, Layout, Rect};
use ratatui::style::{Style, Stylize};
use ratatui::text::{Line, Span};
use ratatui::widgets::{Paragraph, TableState};
use ratatui::Frame;
use std::path::PathBuf;
use std::sync::mpsc;

use crate::api::{ApiClient, PeerInfo, RequestInfo, ServerStatus};
use crate::config::Config;
use crate::event::DataEvent;
use crate::ui;
use crate::widgets::tab_bar::Tab;

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

    pub api: ApiClient,
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
            DataEvent::PeersFetched(Err(e)) => {
                self.error_msg = Some(format!("peers: {}", e));
            }
            DataEvent::RequestsFetched(Ok(resp)) => {
                self.requests = resp.requests;
                if self.request_selected >= self.requests.len() && !self.requests.is_empty() {
                    self.request_selected = self.requests.len() - 1;
                    self.request_state.select(Some(self.request_selected));
                }
            }
            DataEvent::RequestsFetched(Err(e)) => {
                self.error_msg = Some(format!("requests: {}", e));
            }
            DataEvent::StatusFetched(Ok(status)) => {
                self.status = status;
            }
            DataEvent::StatusFetched(Err(e)) => {
                self.error_msg = Some(format!("status: {}", e));
            }
            DataEvent::RequestApproved(_) | DataEvent::RequestDenied(_) | DataEvent::PeerDeleted(_) => {
                self.fetch_all_data();
            }
        }
    }

    pub fn refresh_data(&mut self) {
        self.fetch_all_data();
        self.logs = read_audit_log(&self.audit_log_path);
    }

    fn fetch_all_data(&mut self) {
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
                    self.request_selected =
                        (self.request_selected + 1).min(self.requests.len() - 1);
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
}

pub fn render(frame: &mut Frame, app: &mut App) {
    let area = frame.area();

    let chunks = Layout::vertical([
        Constraint::Length(1),
        Constraint::Length(4),
        Constraint::Min(0),
        Constraint::Length(1),
    ])
    .split(area);

    render_header(frame, chunks[0]);
    crate::widgets::tab_bar::render_tab_bar(frame, chunks[1], app.tab);

    if app.show_help {
        ui::help::render_help(frame, chunks[2]);
    } else {
        match app.tab {
            Tab::Dashboard => {
                ui::dashboard::render_dashboard(
                    frame,
                    chunks[2],
                    app.status.daemon == "running",
                    app.status.wireguard == "ok",
                    &app.status.interface,
                    &app.status.listen_port,
                    app.status.peer_online,
                    app.status.peer_total,
                );
            }
            Tab::Peers => {
                ui::peers::render_peers(
                    frame,
                    chunks[2],
                    &app.peers,
                    &mut app.peer_state,
                    app.peer_selected,
                    app.tick_count,
                );
            }
            Tab::Requests => {
                ui::requests::render_requests(
                    frame,
                    chunks[2],
                    &app.requests,
                    &mut app.request_state,
                    app.request_selected,
                    app.flash,
                );
            }
            Tab::Logs => {
                ui::logs_view::render_logs(frame, chunks[2], &app.logs, 0);
            }
            Tab::Help => {
                ui::help::render_help(frame, chunks[2]);
            }
        }
    }

    render_status_bar(frame, chunks[3], app);
}

fn render_header(frame: &mut Frame, area: Rect) {
    let line = Line::from(Span::styled(
        " ⚡ WG-TUI ".to_string(),
        Style::default()
            .fg(crate::theme::DARK_THEME.primary)
            .bold(),
    ));
    frame.render_widget(Paragraph::new(line), area);
}

fn render_status_bar(frame: &mut Frame, area: Rect, app: &App) {
    let peer_count = format!(
        "{} peers  {} online",
        app.peers.len(),
        app.status.peer_online
    );
    let req_count = format!("{} pending", app.requests.len());

    let help = match app.tab {
        Tab::Peers => "[d] Delete  [↑↓] Navigate  ",
        Tab::Requests => "[a] Approve  [d] Deny  ",
        Tab::Logs => "[j/k] Scroll  ",
        _ => "",
    };

    let error_hint = if app.error_msg.is_some() { "⚠ " } else { "" };

    let full = format!(
        " {} │ {} │ {}{}{}[q] Quit  [tab] Switch  [r] Refresh  [?] Help",
        peer_count, req_count, help,
        error_hint,
        if app.show_help { "[?] Close Help  " } else { "" },
    );

    let line = Line::from(Span::styled(
        full,
        Style::default().fg(crate::theme::DARK_THEME.muted),
    ));
    frame.render_widget(Paragraph::new(line), area);
}

fn read_audit_log(path: &str) -> Vec<String> {
    if path.is_empty() {
        return Vec::new();
    }
    if let Ok(content) = std::fs::read_to_string(path) {
        let lines: Vec<&str> = content.lines().collect();
        let start = if lines.len() > 50 { lines.len() - 50 } else { 0 };
        lines[start..]
            .iter()
            .map(|l| l.to_string())
            .collect()
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
