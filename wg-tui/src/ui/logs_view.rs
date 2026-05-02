use ratatui::layout::Rect;
use ratatui::style::{Style, Stylize};
use ratatui::text::{Line, Span};
use ratatui::widgets::{Block, Borders, Paragraph};
use ratatui::Frame;

use crate::theme::DARK_THEME;

pub fn render_logs(frame: &mut Frame, area: Rect, logs: &[String], scroll_offset: usize) {
    let block = Block::default()
        .borders(Borders::ALL)
        .border_style(Style::default().fg(DARK_THEME.border))
        .title(Span::styled(
            " Audit Log ",
            Style::default().fg(DARK_THEME.primary).bold(),
        ))
        .style(Style::default().bg(DARK_THEME.bg));

    let inner = block.inner(area);
    frame.render_widget(block, area);

    let lines: Vec<Line> = if logs.is_empty() {
        vec![Line::from(Span::styled(
            "  (no log file found or audit log is empty)",
            DARK_THEME.muted,
        ))]
    } else {
        logs.iter()
            .skip(scroll_offset)
            .take(inner.height as usize)
            .map(|entry| {
                let color = if entry.contains("approved") || entry.contains("registered") {
                    DARK_THEME.accent
                } else if entry.contains("rejected")
                    || entry.contains("deleted")
                    || entry.contains("expired")
                {
                    DARK_THEME.danger
                } else if entry.contains("submitted") {
                    DARK_THEME.warning
                } else {
                    DARK_THEME.muted
                };
                Line::from(Span::styled(
                    format!(" {}", entry),
                    Style::default().fg(color),
                ))
            })
            .collect()
    };

    let p = Paragraph::new(lines);
    frame.render_widget(p, inner);
}
