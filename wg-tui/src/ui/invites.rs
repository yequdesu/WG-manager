use crate::app::App;
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

#[derive(Debug, Clone)]
pub struct InviteResult {
    pub token: String,
    pub url: String,
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
        "created" => "◌".to_string(),
        "redeemed" => "✓".to_string(),
        "revoked" => "✗".to_string(),
        "expired" => "⏏".to_string(),
        "deleted" => "🗑".to_string(),
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

static FORM_FIELDS: &[(&str, &str)] = &[
    ("name_hint",   "Label for this invite"),
    ("ttl_hours",   "Hours until expiry"),
    ("dns",         "DNS override (leave blank for default)"),
    ("pool",        "Peer pool name"),
    ("target_role", "Role for peer (user/admin)"),
    ("device",      "Device name hint"),
];
static FORM_FIELD_COUNT: usize = FORM_FIELDS.len();

pub fn render_create_invite_form(frame: &mut Frame, area: Rect, app: &App) {
    let inner = Card::new("Create Invite").render_block(frame, area);

    if let Some(ref result) = app.invite_form_result {
        render_invite_result(frame, inner, result);
        return;
    }

    if app.invite_form_confirm {
        render_invite_confirmation(frame, inner, app);
        return;
    }

    let field_values: [&str; 6] = [
        &app.invite_form_name,
        &app.invite_form_ttl,
        &app.invite_form_dns,
        &app.invite_form_pool,
        &app.invite_form_role,
        &app.invite_form_device,
    ];

    let area_w = inner.width.saturating_sub(4) as usize;
    let mut lines: Vec<Line> = Vec::new();

    for i in 0..FORM_FIELD_COUNT {
        let (name, hint) = FORM_FIELDS[i];
        let value = field_values[i];
        let display_val = if value.is_empty() { "(default)" } else { value };
        let is_sel = i == app.invite_form_field;

        let prefix = if is_sel { ">" } else { " " };
        let label_style = if is_sel {
            Style::default().fg(DARK_THEME.accent)
        } else {
            Style::default().fg(DARK_THEME.muted)
        };
        let val_style = if is_sel {
            Style::default().fg(DARK_THEME.text).bg(DARK_THEME.primary)
        } else {
            Style::default().fg(DARK_THEME.text)
        };

        let field_line = format!(
            " {} {:12} {}",
            prefix,
            name,
            truncate(display_val, area_w.saturating_sub(18))
        );
        let remaining = area_w.saturating_sub(field_line.len());
        let padding = " ".repeat(remaining);

        lines.push(Line::from(vec![
            Span::styled(field_line, label_style),
            Span::styled(padding, val_style),
        ]));

        if is_sel {
            lines.push(Line::from(Span::styled(
                format!("     ── {}", hint),
                Style::default().fg(DARK_THEME.muted),
            )));
        }
    }

    lines.push(Line::from(""));
    lines.push(Line::from(Span::styled(
        " [Tab/↑↓] select field  [Enter] confirm  [Esc] cancel",
        DARK_THEME.muted,
    )));

    Card::new("").render(frame, area, lines);
}

fn render_invite_confirmation(frame: &mut Frame, area: Rect, app: &App) {
    let dns_val = if app.invite_form_dns.is_empty() { "(server default)" } else { app.invite_form_dns.as_str() };
    let pool_val = if app.invite_form_pool.is_empty() { "(none)" } else { app.invite_form_pool.as_str() };
    let role_val = if app.invite_form_role.is_empty() { "user" } else { app.invite_form_role.as_str() };
    let device_val = if app.invite_form_device.is_empty() { "(none)" } else { app.invite_form_device.as_str() };
    let name_val = if app.invite_form_name.is_empty() { "(none)" } else { app.invite_form_name.as_str() };

    let lines = vec![
        Line::from(""),
        Line::from(vec![
            Span::styled("  Confirm invite creation:", DARK_THEME.accent),
        ]),
        Line::from(""),
        Line::from(vec![
            Span::styled("  Name hint:   ", DARK_THEME.muted),
            Span::styled(name_val, DARK_THEME.text),
        ]),
        Line::from(vec![
            Span::styled("  TTL:         ", DARK_THEME.muted),
            Span::styled(format!("{} hours", app.invite_form_ttl), DARK_THEME.text),
        ]),
        Line::from(vec![
            Span::styled("  DNS:         ", DARK_THEME.muted),
            Span::styled(dns_val, DARK_THEME.text),
        ]),
        Line::from(vec![
            Span::styled("  Pool:        ", DARK_THEME.muted),
            Span::styled(pool_val, DARK_THEME.text),
        ]),
        Line::from(vec![
            Span::styled("  Role:        ", DARK_THEME.muted),
            Span::styled(role_val, DARK_THEME.text),
        ]),
        Line::from(vec![
            Span::styled("  Device:      ", DARK_THEME.muted),
            Span::styled(device_val, DARK_THEME.text),
        ]),
        Line::from(""),
        Line::from(Span::styled(
            " [Enter] create  [Esc] back to edit",
            DARK_THEME.muted,
        )),
    ];

    Card::new("Confirm").render(frame, area, lines);
}

fn render_invite_result(frame: &mut Frame, area: Rect, result: &InviteResult) {
    let lines = vec![
        Line::from(""),
        Line::from(vec![
            Span::styled("  Invite created successfully!", DARK_THEME.accent),
        ]),
        Line::from(""),
        Line::from(vec![
            Span::styled("  Token: ", DARK_THEME.muted),
            Span::styled(result.token.as_str(), DARK_THEME.text),
        ]),
        Line::from(""),
        Line::from(vec![
            Span::styled("  Bootstrap URL:", DARK_THEME.muted),
        ]),
        Line::from(vec![
            Span::styled(
                format!("  {}", result.url),
                DARK_THEME.text,
            ),
        ]),
        Line::from(""),
        Line::from(Span::styled(
            " [Enter] or any key to return to list",
            DARK_THEME.muted,
        )),
    ];

    Card::new("Result").render(frame, area, lines);
}
