use ratatui::layout::Rect;
use ratatui::text::{Line, Span};
use ratatui::Frame;

use crate::theme::DARK_THEME;
use crate::widgets::card::{status_line, Card};

pub fn render_dashboard(
    frame: &mut Frame,
    area: Rect,
    daemon_ok: bool,
    wg_ok: bool,
    interface: &str,
    listen_port: &str,
    online: i64,
    total: i64,
) {
    let chunks = ratatui::layout::Layout::vertical([
        ratatui::layout::Constraint::Length(6),
        ratatui::layout::Constraint::Length(6),
        ratatui::layout::Constraint::Min(0),
    ])
    .split(area);

    let top = ratatui::layout::Layout::horizontal([
        ratatui::layout::Constraint::Ratio(1, 2),
        ratatui::layout::Constraint::Ratio(1, 2),
    ])
    .split(chunks[0]);

    let daemon_status = if daemon_ok { "running" } else { "error" };
    let wg_status = if wg_ok { "ok" } else { "error" };

    Card::new("Server").render(
        frame,
        top[0],
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

    Card::new("Key Bindings").render(
        frame,
        top[1],
        vec![
            Line::from(vec![
                Span::styled("  Tab / ←→  ", DARK_THEME.primary),
                Span::styled("Switch tabs", DARK_THEME.text),
            ]),
            Line::from(vec![
                Span::styled("  j / k / ↑↓  ", DARK_THEME.primary),
                Span::styled("Navigate lists", DARK_THEME.text),
            ]),
            Line::from(vec![
                Span::styled("  a / d      ", DARK_THEME.primary),
                Span::styled("Approve / Deny", DARK_THEME.text),
            ]),
            Line::from(vec![
                Span::styled("  r          ", DARK_THEME.primary),
                Span::styled("Force refresh", DARK_THEME.text),
            ]),
        ],
    );

    Card::new("Welcome").render(
        frame,
        chunks[1],
        vec![
            Line::from(Span::styled(
                "  WG-TUI — Ratatui Dashboard for WG-Manager",
                DARK_THEME.text,
            )),
            Line::from(Span::styled(
                "  Navigate to the Peers tab to manage VPN clients.",
                DARK_THEME.muted,
            )),
            Line::from(Span::styled(
                "  Use the Requests tab to approve or deny pending join requests.",
                DARK_THEME.muted,
            )),
        ],
    );
}
