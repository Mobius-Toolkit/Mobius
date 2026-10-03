use dioxus::prelude::*;
use dioxus_primitives::dioxus_attributes::attributes;
use dioxus_primitives::merge_attributes;

#[css_module("/src/components/button/style.css")]
struct Styles;

#[derive(Copy, Clone, PartialEq, Default)]
pub enum ButtonVariant {
    #[default]
    Primary,
    Destructive,
    Outline,
    Ghost,
}

impl ButtonVariant {
    pub fn class(&self) -> &'static str {
        match self {
            ButtonVariant::Primary => "primary",
            ButtonVariant::Destructive => "destructive",
            ButtonVariant::Outline => "outline",
            ButtonVariant::Ghost => "ghost",
        }
    }
}

#[derive(Copy, Clone, PartialEq, Default)]
pub enum ButtonSize {
    Sm,
    #[default]
    Default,
}

impl ButtonSize {
    pub fn class(&self) -> &'static str {
        match self {
            ButtonSize::Sm => "sm",
            ButtonSize::Default => "default",
        }
    }
}

#[component]
pub fn Button(
    #[props(default)] variant: ButtonVariant,
    #[props(default)] size: ButtonSize,
    #[props(extends=GlobalAttributes)]
    #[props(extends=button)]
    attributes: Vec<Attribute>,
    onclick: Option<EventHandler<MouseEvent>>,
    onmousedown: Option<EventHandler<MouseEvent>>,
    onmouseup: Option<EventHandler<MouseEvent>>,
    onkeydown: Option<EventHandler<KeyboardEvent>>,
    children: Element,
) -> Element {
    let base = attributes!(button {
        class: Styles::dx_button,
        "data-style": variant.class(),
        "data-size": size.class(),
    });
    let merged = merge_attributes(vec![base, attributes]);

    rsx! {
        button {
            onclick: move |event| {
                if let Some(f) = &onclick {
                    f.call(event);
                }
            },
            onmousedown: move |event| {
                if let Some(f) = &onmousedown {
                    f.call(event);
                }
            },
            onmouseup: move |event| {
                if let Some(f) = &onmouseup {
                    f.call(event);
                }
            },
            onkeydown: move |event| {
                if let Some(f) = &onkeydown {
                    f.call(event);
                }
            },
            ..merged,
            {children}
        }
    }
}

#[component]
pub fn ButtonLink(
    #[props(default)] variant: ButtonVariant,
    #[props(default)] size: ButtonSize,
    #[props(into)] to: NavigationTarget,
    children: Element,
) -> Element {
    rsx! {
        Link {
            class: Styles::dx_button.to_string(),
            "data-style": variant.class(),
            "data-size": size.class(),
            to,
            {children}
        }
    }
}
