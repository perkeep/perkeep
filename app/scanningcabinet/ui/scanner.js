/*
Copyright 2017 The Camlistore Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

function enableAutoComplete(allTagsFrequency, inputFieldSelector) {
  var split = function(val) {
    return val.split(/,\s*/);
  };

  // Enable autocomplete on form element
  var inputField = $(inputFieldSelector);
  if (inputField.val() != "") {
    inputField.val(inputField.val() + ", ");
  }
  inputField
    // don't navigate away from the field on tab when selecting an item
    .on("keydown", function(event) {
      if (event.keyCode === $.ui.keyCode.TAB &&
          $(this).autocomplete("instance").menu.active ) {
        event.preventDefault();
      }
    })
    .autocomplete({
      minLength: 0,
      source: function(request, response) {
        // delegate back to autocomplete, but extract the last term
        response($.ui.autocomplete.filter(Object.keys(allTagsFrequency),
                                          split(request.term).pop()));
      },
      focus: function() {
        // prevent value inserted on focus
        return false;
      },
      select: function(event, ui) {
        var terms = split(this.value);
        // remove the current input
        terms.pop();
        // add the selected item
        terms.push(ui.item.value);
        // add placeholder to get the comma-and-space at the end
        terms.push("");
        this.value = terms.join(", ");
        return false;
      }
    });
}
