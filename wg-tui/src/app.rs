use ratatui::layout::{Constraint, Layout, Margin, Rect};
use ratatui::style::{Color, Style, Stylize};
use ratatui::text::{Line, Span};
use ratatui::widgets::{Block, BorderType, Borders, Paragraph, TableState};
use ratatui::Frame;
use std::path::PathBuf;
use std::sync::mpsc;

use crate::api::{ApiClient, InviteInfo, PeerInfo, ServerStatus};
use crate::config::Config;
use crate::event::DataEvent;
use crate::theme::DARK_THEME;
use crate::ui::{self, invites::InviteLinkResult};
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

    pub invites: Vec<InviteInfo>,
    pub invite_state: TableState,
    pub invite_selected: usize,

    pub status: ServerStatus,
    pub logs: Vec<String>,
    pub audit_log_error: Option<String>,

    pub tick_count: u64,
    pub last_refresh: i64,

    pub flash: Option<(usize, ui::invites::FlashKind, u64)>,
    pub particles: ParticleSystem,
    pub search_active: bool,
    pub search_query: String,
    pub log_scroll: usize,
    pub window: WindowState,
    pub confirm_delete: bool,
    pub confirm_timer: u16,
    pub pending_text_asteroid: Option<String>,

    // Invite creation form state
    pub invite_form_active: bool,
    pub invite_form_field: usize,
    pub invite_form_name: String,
    pub invite_form_ttl: String,
    pub invite_form_dns: String,
    pub invite_form_pool: String,
    pub invite_form_role: String,
    pub invite_form_device: String,
    pub invite_form_confirm: bool,
    pub invite_form_result: Option<ui::invites::InviteResult>,

    // Invite link view state
    pub invite_link_active: bool,
    pub invite_link_result: Option<InviteLinkResult>,

    // Force-delete confirmation (invites)
    pub confirm_force_delete: bool,
    pub confirm_force_delete_timer: u16,

    // Alias editing state
    pub alias_edit_active: bool,
    pub alias_edit_buffer: String,
    pub alias_edit_pubkey: String,
    pub alias_edit_peer_name: String,

    pub api: ApiClient,
    #[allow(dead_code)]
    pub config: Config,
    pub audit_log_path: String,
    pub rt: tokio::runtime::Handle,
    pub data_tx: mpsc::Sender<DataEvent>,
}

impl App {
    pub fn new(
        config: Config,
        rt: tokio::runtime::Handle,
        data_tx: mpsc::Sender<DataEvent>,
    ) -> Self {
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

            invites: Vec::new(),
            invite_state: TableState::default().with_selected(0),
            invite_selected: 0,

            status: ServerStatus {
                daemon: String::new(),
                wireguard: String::new(),
                interface: String::new(),
                listen_port: String::new(),
                peer_online: 0,
                peer_total: 0,
            },
            logs: Vec::new(),
            audit_log_error: None,

            tick_count: 0,
            last_refresh: 0,

            flash: None,

            particles: ParticleSystem::new(),
            search_active: false,
            search_query: String::new(),
            log_scroll: 0,
            window: WindowState::load(),
            confirm_delete: false,
            confirm_timer: 0,
            pending_text_asteroid: None,

            invite_form_active: false,
            invite_form_field: 0,
            invite_form_name: String::new(),
            invite_form_ttl: "24".to_string(),
            invite_form_dns: String::new(),
            invite_form_pool: String::new(),
            invite_form_role: String::new(),
            invite_form_device: String::new(),
            invite_form_confirm: false,
            invite_form_result: None,

            invite_link_active: false,
            invite_link_result: None,

            confirm_force_delete: false,
            confirm_force_delete_timer: 0,

            alias_edit_active: false,
            alias_edit_buffer: String::new(),
            alias_edit_pubkey: String::new(),
            alias_edit_peer_name: String::new(),

            api,
            config,
            audit_log_path: audit_log,
            rt,
            data_tx,
        }
    }

    pub fn on_tick(&mut self) {
        self.tick_count = self.tick_count.wrapping_add(1);

        if self.alias_edit_active {
            return;
        }

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

        if self.confirm_delete {
            self.confirm_timer += 1;
            if self.confirm_timer > 60 {
                self.confirm_delete = false;
                self.confirm_timer = 0;
            }
        }

        if self.confirm_force_delete {
            self.confirm_force_delete_timer += 1;
            if self.confirm_force_delete_timer > 60 {
                self.confirm_force_delete = false;
                self.confirm_force_delete_timer = 0;
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
            DataEvent::InvitesFetched(Ok(resp)) => {
                self.invites = resp.invites;
                if self.invite_selected >= self.invites.len() && !self.invites.is_empty() {
                    self.invite_selected = self.invites.len() - 1;
                    self.invite_state.select(Some(self.invite_selected));
                }
            }
            DataEvent::InvitesFetched(Err(e)) => self.error_msg = Some(e),
            DataEvent::StatusFetched(Ok(status)) => self.status = status,
            DataEvent::StatusFetched(Err(e)) => self.error_msg = Some(e),
            DataEvent::InviteCreated(Ok(val)) => {
                self.flash = Some((self.invite_selected, ui::invites::FlashKind::Create, 0));
                if self.invite_form_active {
                    let token = val
                        .get("token")
                        .and_then(|v| v.as_str())
                        .unwrap_or("(no token)");
                    let url = val
                        .get("bootstrap_url")
                        .and_then(|v| v.as_str())
                        .map(|s| s.to_string())
                        .unwrap_or_else(|| {
                            // Server did not provide a bootstrap_url; warn and show token only.
                            format!("(no bootstrap_url from server — token: {})", token)
                        });
                    self.invite_form_result = Some(ui::invites::InviteResult {
                        token: token.to_string(),
                        url,
                        command: val
                            .get("command")
                            .and_then(|v| v.as_str())
                            .map(|s| s.to_string())
                            .unwrap_or_else(|| "(no onboarding command from server)".to_string()),
                    });
                }
                self.refresh_data();
            }
            DataEvent::InviteCreated(Err(e)) => {
                if self.invite_form_active {
                    self.invite_form_result = Some(ui::invites::InviteResult {
                        token: String::new(),
                        url: format!("Error: {}", e),
                        command: String::new(),
                    });
                } else {
                    self.error_msg = Some(e);
                }
            }
            DataEvent::InviteRevoked(_) | DataEvent::PeerDeleted(_) => {
                self.refresh_data();
            }
            DataEvent::InviteLinkFetched(Ok(val)) => {
                if self.invite_link_active {
                    if let Some(note) = val.get("note").and_then(|v| v.as_str()) {
                        self.invite_link_result = Some(InviteLinkResult {
                            bootstrap_url: note.to_string(),
                            command: String::new(),
                        });
                        return;
                    }
                    let bootstrap_url = val
                        .get("bootstrap_url")
                        .and_then(|v| v.as_str())
                        .map(|s| s.to_string())
                        .unwrap_or_else(|| {
                            format!(
                                "(no bootstrap_url from server — invite: {})",
                                self.invites
                                    .get(self.invite_selected)
                                    .map(|i| i.id.as_str())
                                    .unwrap_or("?")
                            )
                        });
                    let command = val
                        .get("command")
                        .and_then(|v| v.as_str())
                        .map(|s| s.to_string())
                        .unwrap_or_else(|| "(no command from server)".to_string());
                    self.invite_link_result = Some(InviteLinkResult {
                        bootstrap_url,
                        command,
                    });
                }
            }
            DataEvent::InviteLinkFetched(Err(e)) => {
                if self.invite_link_active {
                    self.invite_link_result = Some(InviteLinkResult {
                        bootstrap_url: format!("Error: {}", e),
                        command: String::new(),
                    });
                }
            }
            DataEvent::InviteForceDeleted(Ok(true)) => {
                self.confirm_force_delete = false;
                self.confirm_force_delete_timer = 0;
                self.refresh_data();
            }
            DataEvent::InviteForceDeleted(Ok(false)) => {
                self.error_msg = Some("force-delete invite failed".to_string());
                self.confirm_force_delete = false;
                self.confirm_force_delete_timer = 0;
            }
            DataEvent::InviteForceDeleted(Err(e)) => {
                self.error_msg = Some(e);
                self.confirm_force_delete = false;
                self.confirm_force_delete_timer = 0;
            }
        }
    }

    pub fn refresh_data(&mut self) {
        match read_audit_log(&self.audit_log_path) {
            Ok(lines) => {
                self.logs = lines;
                self.audit_log_error = None;
            }
            Err(e) => {
                self.logs = Vec::new();
                self.audit_log_error = Some(e);
            }
        }
        let api = self.api.clone();
        let tx = self.data_tx.clone();
        let rt = self.rt.clone();

        rt.spawn(async move {
            let peers = api.get_peers().await;
            let _ = tx.send(DataEvent::PeersFetched(peers));
            let invs = api.get_invites().await;
            let _ = tx.send(DataEvent::InvitesFetched(invs));
            let status = api.get_status().await;
            let _ = tx.send(DataEvent::StatusFetched(status));
        });
    }

    pub fn open_invite_form(&mut self) {
        self.invite_form_active = true;
        self.invite_form_field = 0;
        self.invite_form_name.clear();
        self.invite_form_ttl = "24".to_string();
        self.invite_form_dns.clear();
        self.invite_form_pool.clear();
        self.invite_form_role.clear();
        self.invite_form_device.clear();
        self.invite_form_confirm = false;
        self.invite_form_result = None;
    }

    pub fn submit_invite(&mut self) {
        let ttl: u32 = self.invite_form_ttl.parse().unwrap_or(24);
        let request = crate::api::CreateInviteRequest {
            name_hint: self.invite_form_name.clone(),
            ttl_hours: ttl,
            dns_override: optional_string(&self.invite_form_dns),
            pool_name: optional_string(&self.invite_form_pool),
            target_role: optional_string(&self.invite_form_role),
            device_name: optional_string(&self.invite_form_device),
            max_uses: None,
            labels: None,
        };

        let api = self.api.clone();
        let rt = self.rt.clone();
        let tx = self.data_tx.clone();
        rt.spawn(async move {
            let result = api.create_invite(request).await;
            let _ = tx.send(DataEvent::InviteCreated(result));
        });
    }

    pub fn cancel_invite_form(&mut self) {
        self.invite_form_active = false;
    }

    pub fn start_alias_edit(&mut self, peer_pubkey: &str, peer_name: &str, current_alias: Option<&str>) {
        self.alias_edit_active = true;
        self.alias_edit_pubkey = peer_pubkey.to_string();
        self.alias_edit_peer_name = peer_name.to_string();
        self.alias_edit_buffer = current_alias.unwrap_or("").to_string();
    }

    pub fn submit_alias(&mut self) {
        let pubkey = self.alias_edit_pubkey.clone();
        let alias = self.alias_edit_buffer.clone();
        self.alias_edit_active = false;
        self.alias_edit_buffer.clear();

        let api = self.api.clone();
        let rt = self.rt.clone();
        let tx = self.data_tx.clone();
        rt.spawn(async move {
            let _ = api.set_peer_alias(&pubkey, &alias).await;
            let _ = tx.send(DataEvent::PeerDeleted(Ok(true)));
        });
    }

    pub fn cancel_alias_edit(&mut self) {
        self.alias_edit_active = false;
        self.alias_edit_buffer.clear();
        self.alias_edit_pubkey.clear();
        self.alias_edit_peer_name.clear();
    }

    pub fn revoke_invite(&mut self, id: &str) {
        let selected = self.invite_selected;
        let api = self.api.clone();
        let invite_id = id.to_string();
        let rt = self.rt.clone();
        let tx = self.data_tx.clone();
        rt.spawn(async move {
            let result = api.revoke_invite(&invite_id).await;
            let _ = tx.send(DataEvent::InviteRevoked(result));
        });
        self.flash = Some((selected, ui::invites::FlashKind::Revoke, 0));
    }

    pub fn force_delete_invite(&mut self, id: &str) {
        let api = self.api.clone();
        let invite_id = id.to_string();
        let rt = self.rt.clone();
        let tx = self.data_tx.clone();
        rt.spawn(async move {
            let result = api.force_delete_invite(&invite_id).await;
            let _ = tx.send(DataEvent::InviteForceDeleted(result));
        });
    }

    pub fn fetch_invite_link(&mut self, id: &str, device_name: Option<&str>) {
        self.invite_link_active = true;
        self.invite_link_result = None;
        let api = self.api.clone();
        let invite_id = id.to_string();
        let name = device_name.map(|name| name.to_string());
        let rt = self.rt.clone();
        let tx = self.data_tx.clone();
        rt.spawn(async move {
            let result = api.get_invite_link(&invite_id, name.as_deref()).await;
            let _ = tx.send(DataEvent::InviteLinkFetched(result));
        });
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
        if self.alias_edit_active {
            self.cancel_alias_edit();
        }
        self.tab = self.tab.next();
        self.show_help = false;
    }

    pub fn prev_tab(&mut self) {
        if self.alias_edit_active {
            self.cancel_alias_edit();
        }
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
            Tab::Invites => {
                if !self.invites.is_empty() {
                    self.invite_selected = (self.invite_selected + 1).min(self.invites.len() - 1);
                    self.invite_state.select(Some(self.invite_selected));
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
            Tab::Invites => {
                self.invite_selected = self.invite_selected.saturating_sub(1);
                self.invite_state.select(Some(self.invite_selected));
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
        frame
            .buffer_mut()
            .set_string(area.x, area.y + y, &line, Style::default().bg(bg));
    }
}

pub fn render(frame: &mut Frame, app: &mut App) {
    let term = frame.area();

    // ═══════════════════════════════════════════════════════
    //  Layer 0: 背景层 (全屏 BG_OUTER + 粒子)
    // ═══════════════════════════════════════════════════════
    fill_area(frame, term, DARK_THEME.bg_outer);

    let win = app.window.compute(term);
    if let Some(ref text) = app.pending_text_asteroid.take() {
        let text_copy = text.clone();
        app.particles.spawn_text_asteroid(term, &text_copy);
    }
    app.particles.update(term, win, app.tick_count);
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
    let title_span = Span::styled(
        title.clone(),
        Style::default().fg(DARK_THEME.primary).bold(),
    );
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
            Tab::Invites => render_invites(frame, content_area, app),
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
        Constraint::Length(12),
        Constraint::Length(8),
        Constraint::Min(0),
    ])
    .split(area);

    let top =
        Layout::horizontal([Constraint::Ratio(1, 2), Constraint::Ratio(1, 2)]).split(chunks[0]);

    ui::dashboard::card_server(
        frame,
        top[0],
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
        app.peers
            .iter()
            .filter(|p| {
                p.name
                    .to_lowercase()
                    .contains(&app.search_query.to_lowercase())
                    || p.address.contains(&app.search_query)
            })
            .collect()
    } else {
        app.peers.iter().collect()
    };

    use crate::widgets::card::Card;
    if filtered.is_empty() {
        let hint = if app.peers.is_empty() {
            "No peers connected. Navigate to Invites tab to create an invite."
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

    let detail_area = layout[if app.search_active || !app.search_query.is_empty() {
        2
    } else {
        1
    }];

    let sel = app.peer_selected.min(filtered.len().saturating_sub(1));
    ui::peers::render_peer_list(
        frame,
        list_area,
        &filtered,
        &mut app.peer_state,
        sel,
        app.tick_count,
    );

    if let Some(peer) = filtered.get(sel) {
        ui::peers::render_peer_detail(frame, detail_area, peer, app.tick_count);
    }
}

fn render_invites(frame: &mut Frame, area: Rect, app: &mut App) {
    fill_area(frame, area, DARK_THEME.bg);

    if app.invite_form_active {
        ui::invites::render_create_invite_form(frame, area, app);
        return;
    }

    if app.invite_link_active {
        ui::invites::render_invite_link_view(frame, area, app);
        return;
    }

    if app.confirm_force_delete {
        ui::invites::render_force_delete_confirm(frame, area, app);
        return;
    }

    use crate::widgets::card::Card;
    if app.invites.is_empty() {
        let lines = vec![Line::from(Span::styled(
            "No invites. Press [a] to create a new invite.",
            DARK_THEME.muted,
        ))];
        Card::new("Invites").render(frame, area, lines);
        return;
    }

    let chunks = Layout::vertical([Constraint::Min(4), Constraint::Length(6)]).split(area);

    ui::invites::render_invite_list(
        frame,
        chunks[0],
        &app.invites,
        &mut app.invite_state,
        app.invite_selected,
        app.flash,
    );

    if let Some(inv) = app.invites.get(app.invite_selected) {
        ui::invites::render_invite_detail(frame, chunks[1], inv);
    }
}

fn render_logs(frame: &mut Frame, area: Rect, app: &App) {
    fill_area(frame, area, DARK_THEME.bg);

    use crate::widgets::card::Card;
    if let Some(ref err) = app.audit_log_error {
        let lines = vec![Line::from(Span::styled(
            format!(" {}", err),
            DARK_THEME.danger,
        ))];
        Card::new("Audit Log").render(frame, area, lines);
        return;
    }
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

    if app.alias_edit_active {
        let display = if app.alias_edit_buffer.is_empty() {
            format!(" Alias for {}: ▋", app.alias_edit_peer_name)
        } else {
            format!(
                " Alias for {}: {}▋  [Enter] confirm  [Esc] cancel",
                app.alias_edit_peer_name, app.alias_edit_buffer
            )
        };
        let msg = Line::from(Span::styled(
            display,
            Style::default().fg(DARK_THEME.accent).bg(DARK_THEME.bg),
        ));
        frame.render_widget(Paragraph::new(msg), area);
        return;
    }

    if app.confirm_force_delete {
        let invite_name = app
            .invites
            .get(app.invite_selected)
            .and_then(|i| i.display_name_hint.as_deref())
            .unwrap_or(
                app.invites
                    .get(app.invite_selected)
                    .map(|i| i.id.as_str())
                    .unwrap_or("?"),
            );
        let seconds_left = ((60 - app.confirm_force_delete_timer) / 20) + 1;
        let msg = Line::from(Span::styled(
            format!(
                " Force-delete '{}'? [F] confirm force-delete  [any other key] cancel  (auto-cancel in {}s)",
                invite_name,
                seconds_left,
            ),
            Style::default().fg(DARK_THEME.danger).bg(DARK_THEME.bg),
        ));
        frame.render_widget(Paragraph::new(msg), area);
        return;
    }

    if app.confirm_delete {
        let msg = Line::from(Span::styled(
            format!(
                " Delete '{}'? [d] confirm  [any other key] cancel  (auto-cancel in {}s)",
                app.peers
                    .get(app.peer_selected)
                    .map(|p| p.name.as_str())
                    .unwrap_or("?"),
                (60 - app.confirm_timer) / 20 + 1
            ),
            Style::default().fg(DARK_THEME.danger).bg(DARK_THEME.bg),
        ));
        frame.render_widget(Paragraph::new(msg), area);
        return;
    }

    let peer_count = format!(
        "{} peers  {} online",
        app.peers.len(),
        app.status.peer_online
    );
    let invite_count = format!("{} invites", app.invites.len());

    let search_hint = if app.search_active {
        "[Esc] exit search  ".to_string()
    } else {
        String::new()
    };

    let tab_hint = match app.tab {
        Tab::Peers => format!("{search_hint}[/] Search  [d] Delete  "),
        Tab::Invites => format!("[a] Create invite  [d] Revoke  [v] View link  [F] Force-delete  "),
        Tab::Logs => format!(
            "[PgUp/PgDn] Scroll  {}/{}  ",
            app.log_scroll,
            app.logs.len()
        ),
        _ => search_hint,
    };

    let full = format!(
        " {} │ {} │ {}[q] Quit  [tab] Switch  [r] Refresh  [?] Help",
        peer_count, invite_count, tab_hint,
    );

    let line = Line::from(Span::styled(full, Style::default().fg(DARK_THEME.muted)));
    frame.render_widget(
        Paragraph::new(line).style(Style::default().bg(DARK_THEME.bg)),
        area,
    );
}

fn read_audit_log(path: &str) -> Result<Vec<String>, String> {
    if path.is_empty() {
        return Err("Log path not configured".to_string());
    }
    match std::fs::read_to_string(path) {
        Ok(content) => {
            let lines: Vec<&str> = content.lines().collect();
            let start = if lines.len() > 50 { lines.len() - 50 } else { 0 };
            Ok(lines[start..].iter().map(|l| l.to_string()).collect())
        }
        Err(e) => Err(format!(
            "Cannot read log: {}. Fix permissions: sudo chmod 755 /var/log/wg-mgmt && sudo chmod 644 /var/log/wg-mgmt/wg-mgmt.log",
            e
        )),
    }
}

pub fn read_audit_log_file(path: &str) -> Result<Vec<String>, String> {
    read_audit_log(path)
}

fn optional_string(s: &str) -> Option<String> {
    if s.is_empty() {
        None
    } else {
        Some(s.to_string())
    }
}

fn find_audit_log_path() -> String {
    let paths = [
        "/var/log/wg-mgmt/wg-mgmt.log".to_string(),
        format!(
            "{}/WG-manager/wg-mgmt.log",
            std::env::var("HOME").unwrap_or_else(|_| "/tmp".into())
        ),
    ];

    for p in &paths {
        if PathBuf::from(p).exists() {
            return p.clone();
        }
    }
    // Return new path as default even if it doesn't exist yet
    paths[0].clone()
}
