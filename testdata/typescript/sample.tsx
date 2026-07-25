import * as React from "react";

/**
 * Renders a greeting banner.
 */
export function GreetingBanner(props: { name: string }): JSX.Element {
  return <div className="banner">Hello, {props.name}!</div>;
}

/**
 * An arrow-function React component.
 */
export const SmallBanner = (props: { text: string }): JSX.Element => {
  return <span>{props.text}</span>;
};
