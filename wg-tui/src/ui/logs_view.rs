use ratatui::layout::Rect;
use ratatui::style::Style;
use ratatui::text::{Line, Span};
use ratatui::widgets::Paragraph;
use ratatui::Frame;

use crate::theme::DARK_THEME;
use crate::widgets::card::Card;

pub fn render_logs(frame: &mut Frame, area: Rect, logs: &[String], scroll_offset: usize) {
    let inner = Card::new("Audit Log").render_block(frame, area);

    let lines: Vec<Line> = logs
        .iter()
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
        .collect();

    frame.render_widget(Paragraph::new(lines), inner);
}
