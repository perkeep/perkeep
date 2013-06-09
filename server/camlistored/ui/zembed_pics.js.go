// THIS FILE IS AUTO-GENERATED FROM pics.js
// DO NOT EDIT.

package ui

import "time"

import "camlistore.org/pkg/fileembed"

func init() {
	Files.Add("pics.js", 3386, time.Unix(0, 1370782352000000000), fileembed.String("\n"+
		"// jquery-colorbox browsable photo gallery\n"+
		"\n"+
		"// TODO(mpl): something about camligroup is broken,\n"+
		"// hence why the call to it is commented out.\n"+
		"// Not worth fixing for now as I suppose we may replace\n"+
		"// all this with some closure eye candy eventually?\n"+
		"function addColorboxen() {\n"+
		"  $(document).ready(function() {\n"+
		"    $('li > a').each(function() {\n"+
		"      this.setAttribute('rel', 'camligroup');\n"+
		"    })\n"+
		"    $('a[rel=\"camligroup\"]').colorbox({\n"+
		"      transition:'none',\n"+
		"      width: '75%',\n"+
		"      height: '75%',\n"+
		"      top: '30px',\n"+
		"      open: false,\n"+
		"      href: function() {\n"+
		"        return $(this).parent().find('.camlifile a').attr('href');\n"+
		"      },\n"+
		"      title: function() {\n"+
		"        return $($(this).parent().find('a').get(0)).text();\n"+
		"      }\n"+
		"    });\n"+
		"  });\n"+
		"}\n"+
		"\n"+
		"var titleInput, editLink;\n"+
		"function init() {\n"+
		"  $(document).ready(function() {\n"+
		"    // Before the images are loaded, rewrite the urls to include the square\n"+
		"    // parameter.\n"+
		"    $('li img').each(function() {\n"+
		"      this.src = this.src + '&square=1';\n"+
		"    });\n"+
		"\n"+
		"    if (camliViewIsOwner) {\n"+
		"      $('body').addClass('camliadmin');\n"+
		"\n"+
		"      editLink = $(document.createElement('a'));\n"+
		"      editLink.attr('#');\n"+
		"      editLink.addClass('pics-edit');\n"+
		"      editLink.html('edit title');\n"+
		"      editLink.click(function(e) {\n"+
		"        editTitle();\n"+
		"        e.stopPropagation();\n"+
		"        e.preventDefault();\n"+
		"      });\n"+
		"\n"+
		"      titleInput = $(document.createElement('input'));\n"+
		"      titleInput.blur(function() {\n"+
		"        saveImgTitle($(this));\n"+
		"      });\n"+
		"      titleInput.bind('keypress', function(e) {\n"+
		"        if (e.keyCode == 13) {\n"+
		"          saveImgTitle($(this));\n"+
		"        }\n"+
		"      });\n"+
		"\n"+
		"      $('li').mouseenter(function(e) {\n"+
		"        $(this).find('img').after(editLink);\n"+
		"        editLink.show();\n"+
		"      });\n"+
		"      $('li').mouseleave(function(e) {\n"+
		"        editLink.hide();\n"+
		"      });\n"+
		"    }\n"+
		"  });\n"+
		"}\n"+
		"\n"+
		"function editTitle() {\n"+
		"  var titleSpan = editLink.next();\n"+
		"  titleInput.val(titleSpan.text());\n"+
		"  titleSpan.parent().after(titleInput);\n"+
		"  titleInput.show();\n"+
		"  titleInput.focus();\n"+
		"  titleInput.select();\n"+
		"  titleSpan.hide();\n"+
		"  editLink.hide();\n"+
		"}\n"+
		"\n"+
		"function saveImgTitle(titleInput) {\n"+
		"  var spanTitle = titleInput.parent().find('a span');\n"+
		"  var spanText = spanTitle.text();\n"+
		"  var newVal = titleInput.val();\n"+
		"  if (spanText != newVal) {\n"+
		"    spanTitle.text(newVal);\n"+
		"    var blobRef = titleInput.parent().attr('id').replace(/^camli-/, '');\n"+
		"    camliNewSetAttributeClaim(\n"+
		"      blobRef,\n"+
		"      \"title\",\n"+
		"      newVal,\n"+
		"      {\n"+
		"          success: function() {\n"+
		"              titleInput.hide();\n"+
		"              spanTitle.show();\n"+
		"              spanTitle.effect('highlight', {}, 300);\n"+
		"          },\n"+
		"          fail: function(msg) {\n"+
		"              alert(msg);\n"+
		"          }\n"+
		"      });\n"+
		"  }\n"+
		"  titleInput.hide();\n"+
		"  spanTitle.show();\n"+
		"}\n"+
		"\n"+
		"// Installs jQuery and the colorbox library along with an onload listener\n"+
		"// to fire the init function above.\n"+
		"if (typeof window['jQuery'] == 'undefined') {\n"+
		"  document.write('<link media=\"screen\" rel=\"stylesheet\" href=\"//colorpowered.com/"+
		"colorbox/core/example1/colorbox.css\">');\n"+
		"  document.write('<scr'+'ipt  src=\"//ajax.googleapis.com/ajax/libs/jquery/1.6.2/j"+
		"query.min.js\" onload=\"init()\"></sc'+'ript>');\n"+
		"  document.write('<scr'+'ipt  src=\"//ajax.googleapis.com/ajax/libs/jqueryui/1.8.1"+
		"4/jquery-ui.js\"></sc'+'ript>');\n"+
		"//  document.write('<scr'+'ipt  src=\"//colorpowered.com/colorbox/core/colorbox/jq"+
		"uery.colorbox.js\" onload=\"addColorboxen()\"></sc'+'ript>');\n"+
		"}\n"+
		""))
}
