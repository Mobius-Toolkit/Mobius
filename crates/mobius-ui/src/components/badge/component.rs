use dioxus::prelude::*;

#[css_module("/src/components/badge/style.css")]
struct Styles;

#[derive(Copy, Clone, PartialEq, Default)]
pub enum BadgeVariant {
    #[default]
    Primary,
    Secondary,
    Warning,
}

impl BadgeVariant {
    pub fn class(&self) -> &'static str {
        match self {
            BadgeVariant::Primary => "primary",
            BadgeVariant::Secondary => "secondary",
            BadgeVariant::Warning => "warning",
        }
    }
}

/// The props for the [`Badge`] component.
#[derive(Props, Clone, PartialEq)]
pub struct BadgeProps {
    #[props(default)]
    pub variant: BadgeVariant,

    /// Additional attributes to extend the badge element
    #[props(extends = GlobalAttributes)]
    pub attributes: Vec<Attribute>,

    /// The children of the badge element
    pub children: Element,
}

#[component]
pub fn Badge(props: BadgeProps) -> Element {
    rsx! {
        span {
            class: Styles::dx_badge,
            "data-style": props.variant.class(),
            ..props.attributes,
            {props.children}
        }
    }
}
