use ratatui::layout::{Constraint, Layout, Rect};
use ratatui::style::{Color, Style};
use ratatui::text::{Line, Span};
use ratatui::widgets::{Block, Row, Table, TableState};
use ratatui::Frame;

use crate::api::PeerInfo;
use crate::theme::DARK_THEME;
use crate::widgets::card::Card;

pub fn render_peers(
    frame: &mut Frame,
    area: Rect,
    peers: &[PeerInfo],
    state: &mut TableState,
    selected_idx: usize,
    tick_count: u64,
) {
    let chunks = Layout::vertical([Constraint::Min(0), Constraint::Length(8)])
        .split(area);

    let _header = Row::new(vec![
        "  ", "Name", "IP", "Endpoint", "HS", "Transfer",
    ])
    .style(Style::default().fg(DARK_THEME.muted))
    .height(1);

    let rows: Vec<Row> = peers
        .iter()
        .enumerate()
        .map(|(i, p)| {
            let online = p.online.unwrap_or(false);
            let dot = if online { "●" } else { "○" };
    let dot_color = if online {
                compute_dot_color(tick_count + i as u64)
            } else {
                DARK_THEME.muted
            };
            let name = truncate(&p.name, 20);
            let endpoint = p
                .endpoint
                .as_ref()
                .map(|e| e.as_str())
                .unwrap_or("—");
            let hs = p
                .latest_handshake
                .as_ref()
                .map(|h| format_handshake(h))
                .unwrap_or_else(|| "—".to_string());
            let tx = p
                .transfer_tx
                .as_ref()
                .map(|t| format_bytes(t))
                .unwrap_or_else(|| "—".to_string());
            let rx = p
                .transfer_rx
                .as_ref()
                .map(|t| format_bytes(t))
                .unwrap_or_else(|| "—".to_string());
            let transfer = format!("↓{} ↑{}", rx, tx);

            let style = if i == selected_idx {
                Style::default()
                    .fg(DARK_THEME.text)
                    .bg(DARK_THEME.primary)
            } else {
                Style::default().fg(DARK_THEME.text)
            };

            Row::new(vec![
                Span::styled(dot, Style::default().fg(dot_color)).to_string(),
                name,
                p.address.clone(),
                truncate(endpoint, 22),
                hs,
                transfer,
            ])
            .style(style)
            .height(1)
        })
        .collect();

    let table = Table::new(rows, &[Constraint::Length(3), Constraint::Length(22), Constraint::Length(14), Constraint::Length(24), Constraint::Length(6), Constraint::Min(0)]);

    let list_area = chunks[0];
    frame.render_widget(Block::default(), list_area);
    frame.render_stateful_widget(table, list_area, state);

    if let Some(peer) = peers.get(selected_idx) {
        render_detail(frame, chunks[1], peer, tick_count);
    }
}

fn render_detail(frame: &mut Frame, area: Rect, peer: &PeerInfo, tick_count: u64) {
    let online = peer.online.unwrap_or(false);
    let dot = if online { "●" } else { "○" };
    let _dot_color = if online {
        compute_dot_color(tick_count)
    } else {
        DARK_THEME.muted
    };

    let endpoint = peer.endpoint.as_ref().map(|e| e.as_str()).unwrap_or("—");
    let hs = peer
        .latest_handshake
        .as_ref()
        .map(|h| format_handshake(h))
        .unwrap_or_else(|| "—".to_string());
    let rx = peer
        .transfer_rx
        .as_ref()
        .map(|t| format_bytes(t))
        .unwrap_or_else(|| "—".to_string());
    let tx = peer
        .transfer_tx
        .as_ref()
        .map(|t| format_bytes(t))
        .unwrap_or_else(|| "—".to_string());
    let dns = peer.dns.as_ref().map(|d| d.as_str()).unwrap_or("—");
    let created = peer.created_at.as_ref().map(|c| c.as_str()).unwrap_or("—");

    Card::new(&format!("{} {}", dot, peer.name)).render(
        frame,
        area,
        vec![
            Line::from(vec![
                Span::styled("  PubKey:  ", DARK_THEME.muted),
                Span::styled(truncate(&peer.public_key, 52), DARK_THEME.text),
            ]),
            Line::from(vec![
                Span::styled("  IP:      ", DARK_THEME.muted),
                Span::styled(&peer.address, DARK_THEME.text),
                Span::styled(format!("  HS: {}", hs), DARK_THEME.muted),
            ]),
            Line::from(vec![
                Span::styled("  Endpoint:", DARK_THEME.muted),
                Span::styled(endpoint, DARK_THEME.text),
            ]),
            Line::from(vec![
                Span::styled("  Transfer:", DARK_THEME.muted),
                Span::styled(format!("↓{} ↑{}", rx, tx), DARK_THEME.text),
                Span::styled(format!("  DNS: {}", dns), DARK_THEME.muted),
            ]),
            Line::from(vec![
                Span::styled("  Created: ", DARK_THEME.muted),
                Span::styled(created, DARK_THEME.text),
            ]),
        ],
    );
}

fn compute_dot_color(tick: u64) -> Color {
    let phase = tick as f32 * 0.12;
    let alpha = (phase.sin() * 0.25 + 0.75).clamp(0.0, 1.0);
    Color::Rgb(
        (63.0 * alpha) as u8,
        (185.0 * alpha) as u8,
        (80.0 * alpha) as u8,
    )
}

fn format_handshake(raw: &str) -> String {
    if raw == "0" {
        return "—".into();
    }
    if let Ok(ts) = raw.parse::<i64>() {
        let now = chrono::Utc::now().timestamp();
        let diff = now - ts;
        if diff < 0 {
            return "now".into();
        }
        if diff < 60 {
            format!("{}s", diff)
        } else if diff < 3600 {
            format!("{}m", diff / 60)
        } else if diff < 86400 {
            format!("{}h", diff / 3600)
        } else {
            format!("{}d", diff / 86400)
        }
    } else {
        "—".into()
    }
}

fn format_bytes(raw: &str) -> String {
    if let Ok(n) = raw.parse::<u64>() {
        if n == 0 {
            return "0".into();
        }
        if n >= 1 << 30 {
            format!("{:.1}G", n as f64 / (1u64 << 30) as f64)
        } else if n >= 1 << 20 {
            format!("{:.1}M", n as f64 / (1u64 << 20) as f64)
        } else if n >= 1 << 10 {
            format!("{:.1}K", n as f64 / (1u64 << 10) as f64)
        } else {
            format!("{}B", n)
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
