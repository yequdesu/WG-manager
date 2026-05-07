use ratatui::layout::{Constraint, Rect};
use ratatui::style::Style;
use ratatui::text::{Line, Span};
use ratatui::widgets::{Row, Table, TableState};
use ratatui::Frame;

use crate::api::InviteInfo;
use crate::theme::DARK_THEME;
use crate::widgets::card::Card;

#[derive(Debug, Clone, Copy)]
pub enum FlashKind {
    Create,
    Revoke,
}

pub fn render_invite_list(
    frame: &mut Frame,
    area: Rect,
    invites: &[InviteInfo],
    state: &mut TableState,
    selected_idx: usize,
    flash: Option<(usize, FlashKind, u64)>,
) {
    let inner = Card::new("Invites").render_block(frame, area);

    let rows: Vec<Row> = invites
        .iter()
        .enumerate()
        .map(|(i, inv)| {
            let name_hint = inv
                .display_name_hint
                .as_ref()
                .map(|s| truncate(s, 20))
                .unwrap_or_else(|| "—".to_string());
            let status = inv.status.clone();
            let age = inv
                .created_at
                .as_ref()
                .map(|c| format_time_ago(c))
                .unwrap_or_else(|| "—".to_string());

            let style = if let Some((idx, kind, _)) = flash {
                if i == idx {
                    match kind {
                        FlashKind::Create => Style::default().fg(DARK_THEME.accent),
                        FlashKind::Revoke => Style::default().fg(DARK_THEME.danger),
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
                status_icon(&status),
                truncate(&inv.id, 14),
                name_hint,
                status.clone(),
                age,
            ])
            .style(style)
        })
        .collect();

    let widths = [
        Constraint::Length(3),
        Constraint::Length(16),
        Constraint::Length(22),
        Constraint::Length(12),
        Constraint::Min(0),
    ];
    let table = Table::new(rows, &widths);
    frame.render_stateful_widget(table, inner, state);
}

pub fn render_invite_detail(frame: &mut Frame, area: Rect, inv: &InviteInfo) {
    let expires = inv
        .expires_at
        .as_ref()
        .map(|e| format_time_until(e))
        .unwrap_or_else(|| "—".to_string());
    let issued = inv
        .issued_by
        .as_ref()
        .map(|s| s.as_str())
        .unwrap_or("—");
    let name_hint = inv
        .display_name_hint
        .as_ref()
        .map(|s| s.as_str())
        .unwrap_or("—");

    Card::new("Detail").render(
        frame,
        area,
        vec![
            Line::from(vec![
                Span::styled("  ID:         ", DARK_THEME.muted),
                Span::styled(truncate(&inv.id, 40), DARK_THEME.text),
            ]),
            Line::from(vec![
                Span::styled("  Status:     ", DARK_THEME.muted),
                Span::styled(inv.status.clone(), DARK_THEME.text),
                Span::styled(
                    format!("  Name hint: {}", name_hint),
                    DARK_THEME.muted,
                ),
            ]),
            Line::from(vec![
                Span::styled("  Expires:    ", DARK_THEME.muted),
                Span::styled(expires, DARK_THEME.text),
                Span::styled(
                    format!("  Issued by: {}", issued),
                    DARK_THEME.muted,
                ),
            ]),
            Line::from(vec![
                Span::styled("             ", DARK_THEME.muted),
                Span::styled("[a] Create invite  [d] Revoke invite", DARK_THEME.muted),
            ]),
        ],
    );
}

fn status_icon(status: &str) -> String {
    match status {
        "pending" => "◌".to_string(),
        "used" => "✓".to_string(),
        "revoked" => "✗".to_string(),
        "expired" => "⏏".to_string(),
        _ => "◌".to_string(),
    }
}

fn format_time_ago(raw: &str) -> String {
    if let Ok(dt) = chrono::DateTime::parse_from_rfc3339(raw) {
        let diff = chrono::Utc::now() - dt.with_timezone(&chrono::Utc);
        if diff.num_seconds() < 60 {
            format!("{}s", diff.num_seconds())
        } else if diff.num_minutes() < 60 {
            format!("{}m", diff.num_minutes())
        } else {
            format!("{}h", diff.num_hours())
        }
    } else {
        "—".into()
    }
}

fn format_time_until(raw: &str) -> String {
    if let Ok(dt) = chrono::DateTime::parse_from_rfc3339(raw) {
        let diff = dt.with_timezone(&chrono::Utc) - chrono::Utc::now();
        if diff.num_seconds() < 0 {
            return "expired".into();
        }
        if diff.num_hours() > 0 {
            format!("in {}h {}m", diff.num_hours(), diff.num_minutes() % 60)
        } else {
            format!("in {}m", diff.num_minutes())
        }
    } else {
        "—".into()
    }
}

fn truncate(s: &str, max: usize) -> String {
    if s.chars().count() <= max {
        s.to_string()
    } else {
        format!("{}…", s.chars().take(max - 1).collect::<String>())
    }
}
