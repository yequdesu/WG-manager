use ratatui::layout::{Constraint, Rect};
use ratatui::style::{Style};
use ratatui::text::{Line, Span};
use ratatui::widgets::{Row, Table, TableState};
use ratatui::Frame;

use crate::api::RequestInfo;
use crate::theme::DARK_THEME;
use crate::widgets::card::Card;

#[derive(Debug, Clone, Copy)]
pub enum FlashKind {
    Approve,
    Deny,
}

pub fn render_request_list(
    frame: &mut Frame,
    area: Rect,
    requests: &[RequestInfo],
    state: &mut TableState,
    selected_idx: usize,
    flash: Option<(usize, FlashKind, u64)>,
) {
    let inner = Card::new("Pending Requests").render_block(frame, area);

    let rows: Vec<Row> = requests
        .iter()
        .enumerate()
        .map(|(i, r)| {
            let source = r.source_ip.as_ref()
                .map(|s| truncate(s, 16))
                .unwrap_or_else(|| "—".to_string());
            let age = r.created_at.as_ref()
                .map(|c| format_time_ago(c))
                .unwrap_or_else(|| "—".to_string());

            let style = if let Some((idx, kind, _)) = flash {
                if i == idx {
                    match kind {
                        FlashKind::Approve => Style::default().fg(DARK_THEME.accent),
                        FlashKind::Deny => Style::default().fg(DARK_THEME.danger),
                    }
                } else if i == selected_idx {
                    Style::default().fg(DARK_THEME.text).bg(DARK_THEME.primary)
                } else {
                    Style::default().fg(DARK_THEME.text)
                }
            } else if i == selected_idx {
                Style::default().fg(DARK_THEME.text).bg(DARK_THEME.primary)
            } else {
                Style::default().fg(DARK_THEME.text)
            };

            Row::new(vec![
                "◌".to_string(),
                truncate(&r.hostname, 26),
                r.address.clone(),
                source,
                age,
            ])
            .style(style)
        })
        .collect();

    let widths = [
        Constraint::Length(3),
        Constraint::Length(28),
        Constraint::Length(14),
        Constraint::Length(18),
        Constraint::Min(0),
    ];
    let table = Table::new(rows, &widths);
    frame.render_stateful_widget(table, inner, state);
}

pub fn render_request_detail(
    frame: &mut Frame,
    area: Rect,
    req: &RequestInfo,
) {
    let source = req.source_ip.as_ref().map(|s| s.as_str()).unwrap_or("—");
    let expires = req.expires_at.as_ref()
        .map(|e| format_time_until(e))
        .unwrap_or_else(|| "—".to_string());
    let dns = req.dns.as_ref().map(|d| d.as_str()).unwrap_or("—");

    Card::new("Detail").render(
        frame,
        area,
        vec![
            Line::from(vec![
                Span::styled("  ID:       ", DARK_THEME.muted),
                Span::styled(truncate(&req.id, 40), DARK_THEME.text),
            ]),
            Line::from(vec![
                Span::styled("  Source:   ", DARK_THEME.muted),
                Span::styled(source, DARK_THEME.text),
                Span::styled(format!("  DNS: {}", dns), DARK_THEME.muted),
            ]),
            Line::from(vec![
                Span::styled("  Expires:  ", DARK_THEME.muted),
                Span::styled(expires, DARK_THEME.text),
                Span::styled("  [a] Approve  [d] Deny", DARK_THEME.muted),
            ]),
        ],
    );
}

fn format_time_ago(raw: &str) -> String {
    if let Ok(dt) = chrono::DateTime::parse_from_rfc3339(raw) {
        let diff = chrono::Utc::now() - dt.with_timezone(&chrono::Utc);
        if diff.num_seconds() < 60 { format!("{}s", diff.num_seconds()) }
        else if diff.num_minutes() < 60 { format!("{}m", diff.num_minutes()) }
        else { format!("{}h", diff.num_hours()) }
    } else { "—".into() }
}

fn format_time_until(raw: &str) -> String {
    if let Ok(dt) = chrono::DateTime::parse_from_rfc3339(raw) {
        let diff = dt.with_timezone(&chrono::Utc) - chrono::Utc::now();
        if diff.num_seconds() < 0 { return "expired".into(); }
        if diff.num_hours() > 0 {
            format!("in {}h {}m", diff.num_hours(), diff.num_minutes() % 60)
        } else { format!("in {}m", diff.num_minutes()) }
    } else { "—".into() }
}

fn truncate(s: &str, max: usize) -> String {
    if s.chars().count() <= max { s.to_string() }
    else { format!("{}…", s.chars().take(max - 1).collect::<String>()) }
}
