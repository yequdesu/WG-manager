use ratatui::layout::Rect;
use ratatui::style::Style;
use ratatui::text::{Line, Span};
use ratatui::widgets::Tabs;
use ratatui::Frame;

use crate::theme::DARK_THEME;

#[allow(dead_code)]
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Tab {
    Dashboard,
    Peers,
    Requests,
    Logs,
    Help,
}

impl Tab {
    pub fn all() -> &'static [Tab] {
        &[Tab::Dashboard, Tab::Peers, Tab::Requests, Tab::Logs]
    }

    pub fn label(&self) -> &'static str {
        match self {
            Tab::Dashboard => "Dashboard",
            Tab::Peers => "Peers",
            Tab::Requests => "Requests",
            Tab::Logs => "Logs",
            Tab::Help => "Help",
        }
    }

    pub fn next(self) -> Tab {
        let all = Self::all();
        let idx = all.iter().position(|t| *t == self).unwrap_or(0);
        all[(idx + 1) % all.len()]
    }

    pub fn prev(self) -> Tab {
        let all = Self::all();
        let idx = all.iter().position(|t| *t == self).unwrap_or(0);
        all[(idx + all.len() - 1) % all.len()]
    }
}

pub fn render_tab_bar(frame: &mut Frame, area: Rect, active: Tab) {
    let tabs: Vec<Line> = Tab::all()
        .iter()
        .map(|tab| {
            let label = format!(" {} ", tab.label());
            let style = if *tab == active {
                Style::default()
                    .fg(DARK_THEME.primary)
                    .bg(DARK_THEME.surface)
            } else {
                Style::default().fg(DARK_THEME.muted).bg(DARK_THEME.bg)
            };
            Line::from(Span::styled(label, style))
        })
        .collect();

    let tabs_widget = Tabs::new(tabs)
        .style(Style::default().bg(DARK_THEME.bg))
        .divider("");

    frame.render_widget(tabs_widget, area);
}
