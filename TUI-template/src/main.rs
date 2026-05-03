// ============================================================
// AGENT: REPLACE — Remove this comment block when you've
// customized this file for your project.
//
// This is the entry point. It:
// 1. Creates a tokio async runtime (needed for reqwest API calls)
// 2. Loads config from config.env or environment variables
// 3. Sets up the terminal (raw mode, alternate screen, mouse)
// 4. Runs the TUI event loop
// 5. Cleans up on exit (saves window state, restores terminal)
//
// Customize: config loading, App initialization, panic handler.
// ============================================================

mod api;
mod app;
mod background;
mod config;
mod event;
mod theme;
mod widgets;
mod window;

use crossterm::cursor;
use crossterm::event::{self as crossterm_event, KeyCode, KeyEventKind, KeyModifiers, MouseEventKind};
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

// ============================================================
// AGENT: REPLACE — Define your API data event types.
// ============================================================

fn main() -> io::Result<()> {
    let rt = tokio::runtime::Runtime::new().expect("failed to create tokio runtime");
    let (data_tx, data_rx) = mpsc::channel::<DataEvent>();

    // AGENT: REPLACE — Load your config
    let config = config::Config::load();

    // AGENT: REPLACE — Initialize your App
    let mut app = App::new(config, rt.handle().clone(), data_tx);

    let _guard = panic_handler();

    let mut stdout = io::stdout();
    terminal::enable_raw_mode()?;
    stdout.execute(EnterAlternateScreen)?;
    stdout.execute(cursor::Hide)?;
    stdout.execute(crossterm_event::EnableMouseCapture)?;

    let backend = CrosstermBackend::new(stdout);
    let mut terminal = Terminal::new(backend)?;

    let event_handler = EventHandler::new(50, data_rx);

    // AGENT: REPLACE — Trigger initial data fetch
    app.refresh_data();

    let result = run(&mut terminal, &mut app, &event_handler);

    // AGENT: KEEP — Clean shutdown
    app.on_shutdown();
    terminal::disable_raw_mode()?;
    terminal.backend_mut().execute(crossterm_event::DisableMouseCapture)?;
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
        // Drain any pending data events from the channel
        while let Some(data_event) = handler.try_recv_data() {
            app.apply_data_event(data_event);
        }

        terminal.draw(|frame| app::render(frame, app))?;

        match handler.next() {
            Ok(Event::Key(key)) => {
                if key.kind != KeyEventKind::Press {
                    continue;
                }

                // AGENT: KEEP — Window management (Ctrl+Arrows)
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

                // AGENT: KEEP — Search mode (when active, all chars go to query)
                if app.search_active {
                    match key.code {
                        KeyCode::Esc => { app.search_active = false; app.search_query.clear(); }
                        KeyCode::Backspace => { app.search_query.pop(); }
                        KeyCode::Char(c) => { app.search_query.push(c); }
                        _ => {}
                    }
                    continue;
                }

                // ============================================================
                // AGENT: REPLACE — Your key mappings.
                // Add your application-specific keyboard shortcuts here.
                // Keep: q/quit, Esc/cancel, Tab/switch, ?/help, r/refresh,
                //       =/-/0 zoom, Ctrl+Arrows move, PgUp/PgDn scroll.
                // ============================================================
                match key.code {
                    KeyCode::Char('q') => app.should_quit = true,
                    KeyCode::Esc => { app.should_quit = true; }
                    KeyCode::Tab | KeyCode::Right => app.next_tab(),
                    KeyCode::Left => app.prev_tab(),
                    KeyCode::Char('?') => app.show_help = !app.show_help,
                    KeyCode::Char('r') | KeyCode::Char('R') => app.refresh_data(),
                    KeyCode::Char('=') | KeyCode::Char('+') => app.window.zoom_in(),
                    KeyCode::Char('-') => app.window.zoom_out(),
                    KeyCode::Char('0') => app.window.reset(),
                    KeyCode::Char('/') => app.search_active = !app.search_active,
                    KeyCode::Down | KeyCode::Char('j') => app.select_down(),
                    KeyCode::Up | KeyCode::Char('k') => app.select_up(),
                    KeyCode::Char('1') => app.tab = Tab::Tab1,
                    KeyCode::Char('2') => app.tab = Tab::Tab2,
                    KeyCode::Char('3') => app.tab = Tab::Tab3,
                    KeyCode::Char('4') => app.tab = Tab::Tab4,
                    // AGENT: ADD — Your custom key actions here
                    _ => {}
                }
            }
            Ok(Event::Mouse(mouse)) => {
                // AGENT: KEEP — Mouse scroll for logs/help panels
                match mouse.kind {
                    MouseEventKind::ScrollDown => {
                        if app.tab == Tab::Tab4 { app.log_scroll = app.log_scroll.saturating_add(3); }
                    }
                    MouseEventKind::ScrollUp => {
                        if app.tab == Tab::Tab4 { app.log_scroll = app.log_scroll.saturating_sub(3); }
                    }
                    _ => {}
                }
            }
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

// ============================================================
// AGENT: KEEP — Panic handler (restores terminal on crash)
// ============================================================

fn panic_handler() -> impl Drop {
    struct PanicGuard;
    impl Drop for PanicGuard {
        fn drop(&mut self) {
            let _ = terminal::disable_raw_mode();
            let _ = io::stdout().execute(crossterm_event::DisableMouseCapture);
            let _ = io::stdout().execute(LeaveAlternateScreen);
            let _ = io::stdout().execute(cursor::Show);
        }
    }
    let prev = panic::take_hook();
    panic::set_hook(Box::new(move |info| {
        let _ = terminal::disable_raw_mode();
        let _ = io::stdout().execute(crossterm_event::DisableMouseCapture);
        let _ = io::stdout().execute(LeaveAlternateScreen);
        let _ = io::stdout().execute(cursor::Show);
        prev(info);
    }));
    PanicGuard
}
