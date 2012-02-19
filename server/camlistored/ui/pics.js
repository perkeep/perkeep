
// jquery-colorbox browsable photo gallery

function addColorboxen() {
  $(document).ready(function() {
    $('li > a').each(function() {
      this.setAttribute('rel', 'camligroup');
    })
    $('a[rel="camligroup"]').colorbox({
      transition:'none',
      width: '75%',
      height: '75%',
      top: '30px',
      open: false,
      href: function() {
        return $(this).parent().find('.camlifile a').attr('href');
      },
      title: function() {
        return $($(this).parent().find('a').get(0)).text();
      }
    });
  });
}

var titleInput, editLink;
function init() {
  $(document).ready(function() {
    // Before the images are loaded, rewrite the urls to include the square
    // parameter.
    $('li img').each(function() {
      this.src = this.src + '&square=1';
    });

    if (camliViewIsOwner) {
      $('body').addClass('camliadmin');

      editLink = $(document.createElement('a'));
      editLink.attr('#');
      editLink.addClass('pics-edit');
      editLink.html('edit title');
      editLink.click(function(e) {
        editTitle();
        e.stopPropagation();
        e.preventDefault();
      });

      titleInput = $(document.createElement('input'));
      titleInput.blur(function() {
        saveImgTitle($(this));
      });
      titleInput.bind('keypress', function(e) {
        if (e.keyCode == 13) {
          saveImgTitle($(this));
        }
      });

      $('li').mouseenter(function(e) {
        $(this).find('img').after(editLink);
        editLink.show();
      });
      $('li').mouseleave(function(e) {
        editLink.hide();
      });
    }
  });
}

function editTitle() {
  var titleSpan = editLink.next();
  titleInput.val(titleSpan.text());
  titleSpan.parent().after(titleInput);
  titleInput.show();
  titleInput.focus();
  titleInput.select();
  titleSpan.hide();
  editLink.hide();
}

function saveImgTitle(titleInput) {
  var spanTitle = titleInput.parent().find('a span');
  var spanText = spanTitle.text();
  var newVal = titleInput.val();
  if (spanText != newVal) {
    spanTitle.text(newVal);
    var blobRef = titleInput.parent().attr('id').replace(/^camli-/, '');
    camliNewSetAttributeClaim(
      blobRef,
      "title",
      newVal,
      {
          success: function() {
              titleInput.hide();
              spanTitle.show();
              spanTitle.effect('highlight', {}, 300);
          },
          fail: function(msg) {
              alert(msg);
          }
      });
  }
  titleInput.hide();
  spanTitle.show();
}

// Installs jQuery and the colorbox library along with an onload listener
// to fire the init function above.
if (typeof window['jQuery'] == 'undefined') {
  document.write('<link media="screen" rel="stylesheet" href="//colorpowered.com/colorbox/core/example1/colorbox.css">');
  document.write('<scr'+'ipt  src="//ajax.googleapis.com/ajax/libs/jquery/1.6.2/jquery.min.js" onload="init()"></sc'+'ript>');
  document.write('<scr'+'ipt  src="//ajax.googleapis.com/ajax/libs/jqueryui/1.8.14/jquery-ui.js"></sc'+'ript>');
  document.write('<scr'+'ipt  src="//colorpowered.com/colorbox/core/colorbox/jquery.colorbox.js" onload="addColorboxen()"></sc'+'ript>');
}
