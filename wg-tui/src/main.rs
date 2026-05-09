mod api;
mod app;
mod config;
mod event;
mod theme;
mod ui;
mod widgets;
mod window;

use crossterm::cursor;
use crossterm::event::DisableMouseCapture;
use crossterm::event::EnableMouseCapture;
use crossterm::event::{KeyCode, KeyEventKind, KeyModifiers, MouseEventKind};
use crossterm::terminal::{self, EnterAlternateScreen, LeaveAlternateScreen};
use crossterm::ExecutableCommand;
use ratatui::backend::CrosstermBackend;
use ratatui::Terminal;
use std::io;
use std::panic;
use std::sync::mpsc;

use app::App;
use event::{DataEvent, Event, EventHandler};
use widgets::tab_bar::Tab;

fn main() -> io::Result<()> {
    let rt = tokio::runtime::Runtime::new().expect("failed to create tokio runtime");
    let (data_tx, data_rx) = mpsc::channel::<DataEvent>();

    let config = config::Config::load();
    let mut app = App::new(config, rt.handle().clone(), data_tx);

    let _guard = panic_handler();

    let mut stdout = io::stdout();
    terminal::enable_raw_mode()?;
    stdout.execute(EnterAlternateScreen)?;
    stdout.execute(cursor::Hide)?;
    stdout.execute(EnableMouseCapture)?;

    let backend = CrosstermBackend::new(stdout);
    let mut terminal = Terminal::new(backend)?;

    let event_handler = EventHandler::new(50, data_rx);

    app.refresh_data();
    match app::read_audit_log_file(&app.audit_log_path) {
        Ok(lines) => {
            app.logs = lines;
        }
        Err(e) => {
            app.audit_log_error = Some(e);
        }
    }

    let result = run(&mut terminal, &mut app, &event_handler);

    app.on_shutdown();

    terminal::disable_raw_mode()?;
    terminal.backend_mut().execute(DisableMouseCapture)?;
    terminal.backend_mut().execute(LeaveAlternateScreen)?;
    terminal.backend_mut().execute(cursor::Show)?;

    if let Err(e) = result {
        eprintln!("Error: {}", e);
    }

    Ok(())
}

fn run(
    terminal: &mut Terminal<CrosstermBackend<io::Stdout>>,
    app: &mut App,
    handler: &EventHandler,
) -> io::Result<()> {
    loop {
        while let Some(data_event) = handler.try_recv_data() {
            app.apply_data_event(data_event);
        }

        terminal.draw(|frame| app::render(frame, app))?;

        match handler.next() {
            Ok(Event::Key(key)) => {
                if key.kind != KeyEventKind::Press {
                    continue;
                }
                if app.show_help && key.code != KeyCode::Char('?') && key.code != KeyCode::Esc {
                    app.show_help = false;
                    continue;
                }

                let ctrl = key.modifiers.contains(KeyModifiers::CONTROL);

                if ctrl {
                    match key.code {
                        KeyCode::Up => app.window.move_by(0, -1),
                        KeyCode::Down => app.window.move_by(0, 1),
                        KeyCode::Left => app.window.move_by(-2, 0),
                        KeyCode::Right => app.window.move_by(2, 0),
                        _ => {}
                    }
                    continue;
                }

                if app.search_active {
                    match key.code {
                        KeyCode::Esc => {
                            app.search_active = false;
                            app.search_query.clear();
                        }
                        KeyCode::Backspace => {
                            app.search_query.pop();
                        }
                        KeyCode::Char(c) => {
                            app.search_query.push(c);
                        }
                        _ => {}
                    }
                    continue;
                }

                // ── Alias editing modal ──────────────────────────
                if app.alias_edit_active {
                    match key.code {
                        KeyCode::Esc => {
                            app.cancel_alias_edit();
                        }
                        KeyCode::Enter => {
                            app.submit_alias();
                        }
                        KeyCode::Backspace => {
                            app.alias_edit_buffer.pop();
                        }
                        KeyCode::Char(c) => {
                            app.alias_edit_buffer.push(c);
                        }
                        _ => {}
                    }
                    continue;
                }

                // ── Invite creation form modal ──────────────────────────
                if app.invite_form_active {
                    match key.code {
                        KeyCode::Esc => {
                            if app.invite_form_confirm {
                                app.invite_form_confirm = false;
                            } else {
                                app.cancel_invite_form();
                            }
                        }
                        KeyCode::Enter => {
                            if app.invite_form_result.is_some() {
                                // Dismiss result screen
                                app.invite_form_active = false;
                                app.invite_form_result = None;
                            } else if app.invite_form_confirm {
                                // Submit from confirmation
                                app.invite_form_confirm = false;
                                app.submit_invite();
                            } else {
                                // Go to confirmation
                                app.invite_form_confirm = true;
                            }
                        }
                        KeyCode::Tab | KeyCode::Down | KeyCode::Char('j') => {
                            if !app.invite_form_confirm && app.invite_form_result.is_none() {
                                app.invite_form_field = (app.invite_form_field + 1) % 6;
                            }
                        }
                        KeyCode::Up | KeyCode::Char('k') => {
                            if !app.invite_form_confirm && app.invite_form_result.is_none() {
                                app.invite_form_field = (app.invite_form_field + 5) % 6;
                            }
                        }
                        KeyCode::Backspace => {
                            if !app.invite_form_confirm && app.invite_form_result.is_none() {
                                pop_form_field(app, app.invite_form_field);
                            }
                        }
                        KeyCode::Char(c) => {
                            if !app.invite_form_confirm && app.invite_form_result.is_none() {
                                push_form_field(app, app.invite_form_field, c);
                            } else if app.invite_form_result.is_some() {
                                app.invite_form_active = false;
                                app.invite_form_result = None;
                            }
                        }
                        _ => {
                            // Dismiss result screen on any key
                            if app.invite_form_result.is_some() {
                                app.invite_form_active = false;
                                app.invite_form_result = None;
                            }
                        }
                    }
                    continue;
                }

                // ── Invite link view modal ──────────────────────────
                if app.invite_link_active {
                    match key.code {
                        KeyCode::Esc | KeyCode::Enter | KeyCode::Char('v') | KeyCode::Char('V') => {
                            app.invite_link_active = false;
                            app.invite_link_result = None;
                        }
                        _ => {}
                    }
                    continue;
                }

                // ── Force-delete confirmation ───────────────────────
                if app.confirm_force_delete {
                    match key.code {
                        KeyCode::Char('F') => {
                            if let Some(invite) = app.invites.get(app.invite_selected) {
                                let id = invite.id.clone();
                                app.force_delete_invite(&id);
                            } else {
                                app.confirm_force_delete = false;
                                app.confirm_force_delete_timer = 0;
                                app.error_msg =
                                    Some("selected invite no longer exists".to_string());
                            }
                        }
                        _ => {
                            app.confirm_force_delete = false;
                            app.confirm_force_delete_timer = 0;
                        }
                    }
                    continue;
                }

                match key.code {
                    KeyCode::Char('q') => app.should_quit = true,
                    KeyCode::Esc => {
                        if app.confirm_delete {
                            app.confirm_delete = false;
                            app.confirm_timer = 0;
                        } else if app.search_active {
                            app.search_active = false;
                            app.search_query.clear();
                        } else if app.show_help {
                            app.show_help = false;
                        } else {
                            app.should_quit = true;
                        }
                    }
                    KeyCode::Tab | KeyCode::Right => app.next_tab(),
                    KeyCode::Left => app.prev_tab(),
                    KeyCode::Char('?') => app.show_help = !app.show_help,
                    KeyCode::Char('r') | KeyCode::Char('R') => {
                        app.refresh_data();
                    }
                    KeyCode::Char('=') | KeyCode::Char('+') => app.window.zoom_in(),
                    KeyCode::Char('-') => app.window.zoom_out(),
                    KeyCode::Char('0') => app.window.reset(),
                    KeyCode::Char('/') => {
                        if app.tab == Tab::Peers {
                            app.search_active = !app.search_active;
                            if !app.search_active {
                                app.search_query.clear();
                            }
                        }
                    }
                    KeyCode::Down | KeyCode::Char('j') => app.select_down(),
                    KeyCode::Up | KeyCode::Char('k') => app.select_up(),
                    KeyCode::PageDown => {
                        if app.tab == Tab::Logs {
                            app.log_scroll = app.log_scroll.saturating_add(20);
                        }
                    }
                    KeyCode::PageUp => {
                        if app.tab == Tab::Logs {
                            app.log_scroll = app.log_scroll.saturating_sub(20);
                        }
                    }
                    KeyCode::Home => {
                        if app.tab == Tab::Logs {
                            app.log_scroll = 0;
                        }
                    }
                    KeyCode::End => {
                        if app.tab == Tab::Logs {
                            app.log_scroll = app.logs.len().saturating_sub(10);
                        }
                    }
                    KeyCode::Char('d') | KeyCode::Char('D') => {
                        if app.tab == Tab::Peers && !app.peers.is_empty() {
                            if app.confirm_delete {
                                let name = app.peers[app.peer_selected].name.clone();
                                app.delete_peer(&name);
                                app.confirm_delete = false;
                                app.confirm_timer = 0;
                            } else {
                                app.confirm_delete = true;
                                app.confirm_timer = 0;
                            }
                        } else if app.tab == Tab::Invites && !app.invites.is_empty() {
                            let id = app.invites[app.invite_selected].id.clone();
                            app.revoke_invite(&id);
                        }
                    }
                    KeyCode::Char('e') | KeyCode::Char('E') => {
                        if app.tab == Tab::Peers && !app.peers.is_empty() && !app.confirm_delete && !app.search_active {
                            let (pubkey, name, alias) = {
                                let peer = &app.peers[app.peer_selected];
                                (peer.public_key.clone(), peer.name.clone(), peer.alias.clone())
                            };
                            app.start_alias_edit(&pubkey, &name, alias.as_deref());
                        }
                    }
                    KeyCode::Char('y') | KeyCode::Char('Y') => {
                        if app.confirm_delete && app.tab == Tab::Peers && !app.peers.is_empty() {
                            let name = app.peers[app.peer_selected].name.clone();
                            app.delete_peer(&name);
                            app.confirm_delete = false;
                            app.confirm_timer = 0;
                        } else if app.tab == Tab::Dashboard {
                            app.pending_text_asteroid = Some("YeQuDesu".into());
                        }
                    }
                    KeyCode::Char('c') | KeyCode::Char('C') => {
                        if app.tab == Tab::Dashboard {
                            app.pending_text_asteroid = Some("CyDlen".into());
                        }
                    }
                    KeyCode::Char('a') | KeyCode::Char('A') => {
                        if app.tab == Tab::Invites {
                            app.open_invite_form();
                        }
                    }
                    KeyCode::Char('v') | KeyCode::Char('V') => {
                        if app.tab == Tab::Invites && !app.invites.is_empty() {
                            let device_name = app.invites[app.invite_selected].device_name.clone();
                            let id = app.invites[app.invite_selected].id.clone();
                            app.fetch_invite_link(&id, device_name.as_deref());
                        }
                    }
                    KeyCode::Char('F') => {
                        if app.tab == Tab::Invites && !app.invites.is_empty() {
                            if app.confirm_force_delete {
                                let id = app.invites[app.invite_selected].id.clone();
                                app.force_delete_invite(&id);
                            } else {
                                app.confirm_force_delete = true;
                                app.confirm_force_delete_timer = 0;
                            }
                        }
                    }
                    KeyCode::Char('1') => app.tab = Tab::Dashboard,
                    KeyCode::Char('2') => app.tab = Tab::Peers,
                    KeyCode::Char('3') => app.tab = Tab::Invites,
                    KeyCode::Char('4') => app.tab = Tab::Logs,
                    _ => {
                        if app.confirm_delete {
                            app.confirm_delete = false;
                            app.confirm_timer = 0;
                        }
                    }
                }
            }
            Ok(Event::Mouse(mouse)) => match mouse.kind {
                MouseEventKind::ScrollDown => {
                    if app.tab == Tab::Logs {
                        app.log_scroll = app.log_scroll.saturating_add(3);
                    }
                }
                MouseEventKind::ScrollUp => {
                    if app.tab == Tab::Logs {
                        app.log_scroll = app.log_scroll.saturating_sub(3);
                    }
                }
                _ => {}
            },
            Ok(Event::Tick) => app.on_tick(),
            Ok(_) => {}
            Err(e) => app.error_msg = Some(e.to_string()),
        }

        if app.should_quit {
            break;
        }
    }
    Ok(())
}

fn push_form_field(app: &mut App, field: usize, c: char) {
    match field {
        0 => app.invite_form_name.push(c),
        1 => app.invite_form_ttl.push(c),
        2 => app.invite_form_dns.push(c),
        3 => app.invite_form_pool.push(c),
        4 => app.invite_form_role.push(c),
        5 => app.invite_form_device.push(c),
        _ => {}
    }
}

fn pop_form_field(app: &mut App, field: usize) {
    match field {
        0 => {
            app.invite_form_name.pop();
        }
        1 => {
            app.invite_form_ttl.pop();
        }
        2 => {
            app.invite_form_dns.pop();
        }
        3 => {
            app.invite_form_pool.pop();
        }
        4 => {
            app.invite_form_role.pop();
        }
        5 => {
            app.invite_form_device.pop();
        }
        _ => {}
    }
}

fn panic_handler() -> impl Drop {
    struct PanicGuard;
    impl Drop for PanicGuard {
        fn drop(&mut self) {
            let _ = terminal::disable_raw_mode();
            let _ = io::stdout().execute(DisableMouseCapture);
            let _ = io::stdout().execute(LeaveAlternateScreen);
            let _ = io::stdout().execute(cursor::Show);
        }
    }
    let prev = panic::take_hook();
    panic::set_hook(Box::new(move |info| {
        let _ = terminal::disable_raw_mode();
        let _ = io::stdout().execute(DisableMouseCapture);
        let _ = io::stdout().execute(LeaveAlternateScreen);
        let _ = io::stdout().execute(cursor::Show);
        prev(info);
    }));
    PanicGuard
}
