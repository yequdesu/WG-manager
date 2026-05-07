#![allow(dead_code)]

use ratatui::layout::Rect;
use ratatui::style::Style;
use ratatui::text::Line;
use ratatui::widgets::Paragraph;
use ratatui::Frame;

use crate::theme::DARK_THEME;

pub fn status_dot(frame: &mut Frame, area: Rect, online: bool, tick_count: u64) {
    let color = if online {
        let phase = tick_count as f32 * 0.1;
        let alpha = (phase.sin() * 0.35 + 0.65).clamp(0.0, 1.0);
        ratatui::style::Color::Rgb(
            (63.0 * alpha) as u8,
            (185.0 * alpha) as u8,
            (80.0 * alpha) as u8,
        )
    } else {
        DARK_THEME.muted
    };

    let symbol = if online { "●" } else { "○" };
    let text = Line::from(symbol).style(Style::default().fg(color));
    frame.render_widget(Paragraph::new(text), area);
}
