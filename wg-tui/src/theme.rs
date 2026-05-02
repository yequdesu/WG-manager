#![allow(dead_code)]

use ratatui::style::Color;

pub struct Theme {
    pub bg: Color,
    pub surface: Color,
    pub border: Color,
    pub primary: Color,
    pub accent: Color,
    pub warning: Color,
    pub danger: Color,
    pub text: Color,
    pub muted: Color,
    pub sparkline: Color,
    pub sparkline_bg: Color,
}

pub const DARK_THEME: Theme = Theme {
    bg: Color::Rgb(13, 17, 23),
    surface: Color::Rgb(22, 27, 34),
    border: Color::Rgb(48, 54, 61),
    primary: Color::Rgb(88, 166, 255),
    accent: Color::Rgb(63, 185, 80),
    warning: Color::Rgb(210, 153, 34),
    danger: Color::Rgb(248, 81, 73),
    text: Color::Rgb(201, 209, 217),
    muted: Color::Rgb(139, 148, 158),
    sparkline: Color::Rgb(88, 166, 255),
    sparkline_bg: Color::Rgb(22, 27, 34),
};

pub const HEADER_STYLE: (Color, Color) = (DARK_THEME.primary, DARK_THEME.bg);
pub const TAB_ACTIVE_STYLE: (Color, Color) = (DARK_THEME.primary, DARK_THEME.surface);
pub const TAB_INACTIVE_STYLE: (Color, Color) = (DARK_THEME.muted, DARK_THEME.bg);
pub const STATUS_BAR_STYLE: (Color, Color) = (DARK_THEME.muted, DARK_THEME.bg);
pub const CARD_BORDER_STYLE: Color = DARK_THEME.border;
pub const HIGHLIGHT_STYLE: (Color, Color) = (DARK_THEME.bg, DARK_THEME.primary);
