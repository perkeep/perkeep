
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

function init() {
  $(document).ready(function() {
    if (camliViewIsOwner) {
      $('body').addClass('camliadmin');

      var titleInput = $(document.createElement('input'));
      titleInput.blur(function() {
        var spanTitle = $(this).parent().find('a span');
        var spanText = spanTitle.text();
        var newVal = $(this).val();
        if (spanText != newVal) {
          spanTitle.text(newVal);
          var blobRef = $(this).parent().attr('id').replace(/^camli-/, '');
          window.console.log('blobRef', blobRef);
          camliNewSetAttributeClaim(
            blobRef,
            "title",
            newVal,
            {
                success: function() {
                    var elapsedMs = new Date().getTime() - startTime.getTime();
                    setTimeout(function() {
                        inputTitle.disabled = false;
                        btnSaveTitle.disabled = false;
                        buildPermanodeUi();
                    }, Math.max(250 - elapsedMs, 0));
                },
                fail: function(msg) {
                    alert(msg);
                    inputTitle.disabled = false;
                    btnSaveTitle.disabled = false;
                }
            });
        }
        $(this).hide();
        spanTitle.show();
      });

      $('li a span').mouseover(function() {
        titleInput.val($(this).text());
        $(this).parent().after(titleInput);
        titleInput.show();
        titleInput.focus();
        titleInput.select();
        $(this).hide();
      });
    }
  });
}

// Installs jQuery and the colorbox library along with an onload listener
// to fire the init function above.
if (typeof window['jQuery'] == 'undefined') {
  document.write('<link media="screen" rel="stylesheet" href="//colorpowered.com/colorbox/core/example1/colorbox.css">');
  document.write('<scr'+'ipt  src="//ajax.googleapis.com/ajax/libs/jquery/1.6.1/jquery.min.js" onload="init()"></sc'+'ript>');
  document.write('<scr'+'ipt  src="//colorpowered.com/colorbox/core/colorbox/jquery.colorbox.js" onload="addColorboxen()"></sc'+'ript>');
}
