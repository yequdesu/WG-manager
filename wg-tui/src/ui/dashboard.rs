use ratatui::layout::Rect;
use ratatui::text::{Line, Span};
use ratatui::Frame;

use crate::theme::DARK_THEME;
use crate::widgets::card::{Card, status_line};

pub fn card_server(
    frame: &mut Frame,
    area: Rect,
    daemon_ok: bool,
    wg_ok: bool,
    interface: &str,
    listen_port: &str,
    online: i64,
    total: i64,
) {
    let daemon_status = if daemon_ok { "running" } else { "error" };
    let wg_status = if wg_ok { "ok" } else { "error" };

    Card::new("Server").render(
        frame,
        area,
        vec![
            status_line(daemon_status, "Daemon     "),
            status_line(wg_status, "WireGuard  "),
            status_line("active", &format!("Interface  {}:{}", interface, listen_port)),
            status_line(
                if online > 0 { "active" } else { "inactive" },
                &format!("Peers      {} / {} online", online, total),
            ),
        ],
    );
}

pub fn card_bindings(frame: &mut Frame, area: Rect) {
    Card::new("Shortcuts").render(
        frame,
        area,
        vec![
            Line::from(vec![
                Span::styled("  Tab / ←→  ", DARK_THEME.primary),
                Span::styled("Switch tabs", DARK_THEME.text),
            ]),
            Line::from(vec![
                Span::styled("  j/k/↑↓    ", DARK_THEME.primary),
                Span::styled("Navigate lists", DARK_THEME.text),
            ]),
            Line::from(vec![
                Span::styled("  a / d     ", DARK_THEME.primary),
                Span::styled("Approve / Deny", DARK_THEME.text),
            ]),
            Line::from(vec![
                Span::styled("  r         ", DARK_THEME.primary),
                Span::styled("Force refresh", DARK_THEME.text),
            ]),
        ],
    );
}

pub fn card_welcome(frame: &mut Frame, area: Rect) {
    Card::new("Welcome").render(
        frame,
        area,
        vec![
            Line::from(Span::styled(
                "  WG-TUI · Ratatui Dashboard for WG-Manager",
                DARK_THEME.text,
            )),
            Line::from(Span::styled(
                "  ▼ Peers tab to manage VPN clients",
                DARK_THEME.muted,
            )),
            Line::from(Span::styled(
                "  ▼ Requests tab to approve or deny pending join requests",
                DARK_THEME.muted,
            )),
        ],
    );
}
