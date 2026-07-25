import * as React from "react";

/**
 * Renders a greeting banner.
 */
export function GreetingBanner(props) {
  return <div className="banner">Hello, {props.name}!</div>;
}

/**
 * An arrow-function React component.
 */
export const SmallBanner = (props) => {
  return <span>{props.text}</span>;
};
