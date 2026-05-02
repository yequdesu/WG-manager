mod api;
mod app;
mod config;
mod event;
mod theme;
mod ui;
mod widgets;

use crossterm::cursor;
use crossterm::event::{KeyCode, KeyEventKind};
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

    let backend = CrosstermBackend::new(stdout);
    let mut terminal = Terminal::new(backend)?;

    let event_handler = EventHandler::new(50, data_rx);

    app.refresh_data();
    app.logs = app::read_audit_log_file(&app.audit_log_path);

    let result = run(&mut terminal, &mut app, &event_handler);

    terminal::disable_raw_mode()?;
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
                if app.show_help && key.code != KeyCode::Char('?') && key.code != KeyCode::Esc
                {
                    app.show_help = false;
                    continue;
                }
                match key.code {
                    KeyCode::Char('q') | KeyCode::Esc => {
                        app.should_quit = true;
                    }
                    KeyCode::Tab | KeyCode::Right => app.next_tab(),
                    KeyCode::Left => app.prev_tab(),
                    KeyCode::Char('?') => app.show_help = !app.show_help,
                    KeyCode::Char('r') | KeyCode::Char('R') => {
                        app.refresh_data();
                        app.logs = app::read_audit_log_file(&app.audit_log_path);
                    }
                    KeyCode::Down | KeyCode::Char('j') => app.select_down(),
                    KeyCode::Up | KeyCode::Char('k') => app.select_up(),
                    KeyCode::Char('d') | KeyCode::Char('D') => {
                        if app.tab == Tab::Peers && !app.peers.is_empty() {
                            let name = app.peers[app.peer_selected].name.clone();
                            app.delete_peer(&name);
                        } else if app.tab == Tab::Requests && !app.requests.is_empty() {
                            let id = app.requests[app.request_selected].id.clone();
                            app.deny_request(&id);
                        }
                    }
                    KeyCode::Char('a') | KeyCode::Char('A') => {
                        if app.tab == Tab::Requests && !app.requests.is_empty() {
                            let id = app.requests[app.request_selected].id.clone();
                            app.approve_request(&id);
                        }
                    }
                    KeyCode::Char('1') => app.tab = Tab::Dashboard,
                    KeyCode::Char('2') => app.tab = Tab::Peers,
                    KeyCode::Char('3') => app.tab = Tab::Requests,
                    KeyCode::Char('4') => app.tab = Tab::Logs,
                    _ => {}
                }
            }
            Ok(Event::Tick) => {
                app.on_tick();
            }
            Ok(_) => {}
            Err(e) => {
                app.error_msg = Some(e.to_string());
            }
        }

        if app.should_quit {
            break;
        }
    }
    Ok(())
}

fn panic_handler() -> impl Drop {
    struct PanicGuard;
    impl Drop for PanicGuard {
        fn drop(&mut self) {
            let _ = terminal::disable_raw_mode();
            let _ = io::stdout().execute(LeaveAlternateScreen);
            let _ = io::stdout().execute(cursor::Show);
        }
    }
    let prev = panic::take_hook();
    panic::set_hook(Box::new(move |info| {
        let _ = terminal::disable_raw_mode();
        let _ = io::stdout().execute(LeaveAlternateScreen);
        let _ = io::stdout().execute(cursor::Show);
        prev(info);
    }));
    PanicGuard
}
