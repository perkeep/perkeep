# Web Application Styleguide

**Note:** *The following statements represent how we think things
should be, not how they are. The Web UI is just getting started and doesn't
adhere to all of these goals yet. New code should, though.*


## Architecture

The Camlistore web application is an "[AJAX][]"-style web app that interacts
with Camlistore servers just as any other client would.  It speaks the same
HTTP APIs as the Android and iOS clients, for example. We avoid creating APIs
that are specific to one client, instead preferring to generalize functionality
such that all current clients and even unknown future clients can make use of
it.

The web application is written almost entirely in JavaScript. We make no effort
to "[degrade gracefully][]" in the absence of JavaScript or CSS support.

[AJAX]: http://en.wikipedia.org/wiki/Ajax_(programming)
[degrade gracefully]: http://www.w3.org/wiki/Graceful_degredation_versus_progressive_enhancement

## Paradigm

Though we are architected mostly as a "[single-page application][]", we make
extensive use of URLs via [pushState()][].  In general every unique view in the
application has a URL that can be used to revisit that view.

In the same vein, although we are an interactive application, we make
appropriate use of web platform primitives where they exist. We use &lt;a&gt;
tags for clickable things that navigate, so that browser tools like "Open in
new tab" and "Copy link" work as users would expect. Similarly, when we want to
display text, we use HTML text rather than  &lt;canvas&gt; or &lt;img&gt; tags
so that selection and find-in-page work.

[single-page application]: http://en.wikipedia.org/wiki/Single-page_application
[pushState()]: https://developer.mozilla.org/en-US/docs/Web/Guide/API/DOM/Manipulating_the_browser_history

## Stack

We use [Closure](https://code.google.com/p/closure-library/) as our "standard
library". It has a really wide and deep collection of well-designed and
implemented utilities. We model our own application logic (e.g., SearchSession)
as Closure-style classes.

For the UI we are using [React](http://facebook.github.io/react/) because it is
awesome. Some older parts of the code use Closure's UI framework; those will be
going away.


## Style

### Tabs, not spaces
For consistency with Go, and because it makes worrying about minor formatting
details like line-wrap style impossible. Some old code still uses spaces. If
you are going to be doing significant work on a file that uses spaces, just
convert it to tabs in a commit before starting.

### No max line length
It's non-sensical with tabs. Configure your editor to wrap lines nicely, and
only insert physical line breaks to separate major thoughts. Sort of where
you'd put a period in English text. Look at newer code like
server_connection.js for examples.

### Continuation lines are indented with a single tab
Always. No worrying about lining things up vertically with the line above.

### Type annotations
We don't currently using the Closure compiler, and there's some debate about
whether we ever will. However, if you're going to have a comment that describes
the type of some identifier, you may as well make it rigorous. Use the Closure
[type annotations](https://developers.google.com/closure/compiler/docs/js-for-compiler)
for this.

### Other formatting minutiae
Everything else generally follows the [Google JavaScript
Styleguide](http://google-styleguide.googlecode.com/svn/trunk/javascriptguide.xml).
Or you can just look at the surrounding code.


## Compatibility

We target the last two stable versions of Desktop Chrome, Firefox, Safari, IE.
We also target the last two stable versions of Safari and Chrome on Android and
iOS tablets.
