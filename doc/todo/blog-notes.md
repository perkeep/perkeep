Thoughts on storing a blog in Perkeep and serving it from the
publish handler.

* a blog is a permanode

* a blog post is a permanode

* the post's permanode is a member of the blog's permanode

* views of the blog we'd like:

  1. reverse chronological (typical blog view)

     - needs efficient reverse time index on membership.

     - membership is currently "add-attribute" claims on parent
       permanode, implying that a large/old blog with thousands
       of posts will involve resolving the attributes of
       the blog's permanode all the time.  we need to either make
       that efficient (caching it as a function of last mutation
       claim to that permanode?) or find a different model
       for memberships.  I'm inclined to say keep the model
       and make it fast.

  2. forward chronological by date posted.  (year, month, day view)

     - denormalization question.  the date of the blog post should
       be an attribute of the post's permanode (defaulting to the
       date of the first/last claim mutation on it), but for efficient
       indexing we'll need to either mirror this into the blog
       permanode's attributes, or have another attribute on the
       blog post that we can prefix scan that includes as the prefix
       the blog's permanode.  the latter is probably ideal so
       blog posts can be cross-posted to multiple blogs, and keeps
       the number of attributes on the blog permanode lower.

         e.g. blog post can have (add-)attributes:

            "inparent" => "<blog-permanode>"
