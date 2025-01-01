package com.Fanbbs.controller;

import com.Fanbbs.common.*;
import com.alibaba.fastjson.JSON;
import com.alibaba.fastjson.JSONArray;
import com.alibaba.fastjson.JSONObject;
import com.Fanbbs.entity.*;
import com.Fanbbs.service.*;
import com.auth0.jwt.interfaces.DecodedJWT;
import net.dreamlu.mica.xss.core.XssCleanIgnore;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.beans.factory.annotation.Value;
import org.springframework.data.redis.core.RedisTemplate;
import org.springframework.stereotype.Component;
import org.springframework.stereotype.Controller;
import org.springframework.web.bind.annotation.*;

import javax.servlet.http.HttpServletRequest;
import java.time.Duration;
import java.time.LocalDate;
import java.time.LocalDateTime;
import java.time.LocalTime;
import java.util.*;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/**
 * 控制层
 * TypechoCommentsController
 *
 * @author buxia97
 * @date 2021/11/29
 */
@Component
@Controller
@RequestMapping(value = "/comments")
public class CommentsController {

    @Autowired
    CommentsService service;

    @Autowired
    private ArticleService contentsService;

    @Autowired
    private UserlogService userlogService;

    @Autowired
    private UsersService usersService;


    @Autowired
    private CommentlikeService commentlikeService;

    @Autowired
    private HeadpictureService headpictureService;

    @Autowired
    private ApiconfigService apiconfigService;

    @Autowired
    private TaskService taskService;

    @Autowired
    private RedisTemplate redisTemplate;


    @Autowired
    private PushService pushService;

    @Autowired
    private InboxService inboxService;

    @Value("${web.prefix}")
    private String dataprefix;

    RedisHelp redisHelp = new RedisHelp();
    ResultAll Result = new ResultAll();
    UserStatus UStatus = new UserStatus();
    baseFull baseFull = new baseFull();
    TaskUtils taskUtils = new TaskUtils();


    /***
     * 评论列表
     *
     */
    @RequestMapping(value = "/list")
    @ResponseBody
    public String list(@RequestParam(value = "page", required = false, defaultValue = "1") Integer page, @RequestParam(value = "limit", required = false, defaultValue = "10") Integer limit, @RequestParam(value = "id", required = false) Integer id, @RequestParam(value = "parent", required = false) Integer parent, @RequestParam(value = "all", required = false) Integer all, @RequestParam(value = "params", required = false) String params, @RequestParam(value = "searchKey", required = false) String searchKey, @RequestParam(value = "order", defaultValue = "created desc") String order, HttpServletRequest request) {
        try {
            // 获取用户信息
            String token = request.getHeader("Authorization");
            Users user = getUserFromToken(token);

            // 获取评论信息
            Comments comments = getComments(id, parent, all, params);
            PageList<Comments> commentsPageList = service.selectPage(comments, page, limit, searchKey, order);
            List<Comments> commentsList = commentsPageList.getList();

            JSONArray dataList = new JSONArray();
            for (Comments _comments : commentsList) {
                // 获取文章信息
                Article article = getArticle(id, _comments.getCid());
                Map<String, Object> data = getDataFromComments(_comments);

                // 获取评论用户信息
                Users commentUser = usersService.selectByKey(_comments.getUid());
                if (commentUser == null || commentUser.getUid() == null) continue;
                Map<String, Object> dataUser = getDataFromUser(commentUser);

                // 检查用户是否点赞
                Integer isLike = checkUserLike(user, _comments);

                // 格式化评论中的图片信息
                List images = getImagesFromComments(_comments);

                // 将文章信息加入到评论数据中
                Map<String, Object> articleData = getArticleData(article);

                // 获取父评论信息
                Map<String, Object> parentCommentData = getParentCommentData(_comments);

                // 获取子评论
                Map<String, Object> subMapData = getSubDataList(_comments, article);

                // 将数据整合到主数据中
                assembleData(data, images, articleData, isLike, dataUser, parentCommentData, subMapData);

                dataList.add(data);
            }

            // 构建返回结果
            Map<String, Object> data = new HashMap<>();
            data.put("page", page);
            data.put("limit", limit);
            data.put("data", dataList);
            data.put("count", dataList.size());
            data.put("total", service.total(comments, searchKey));
            return Result.getResultJson(200, "获取成功", data);

        } catch (Exception e) {
            e.printStackTrace();
            return Result.getResultJson(400, "接口异常", null);
        }
    }

    // 辅助函数，从token中获取用户信息
    private Users getUserFromToken(String token) {
        Users user = new Users();
        if (token != null && !token.isEmpty()) {
            DecodedJWT verify = JWT.verify(token);
            user = usersService.selectByKey(Integer.parseInt(verify.getClaim("aud").asString()));
        }
        return user;
    }

    // 辅助函数，根据请求参数获取评论信息
    private Comments getComments(Integer id, Integer parent, Integer all, String params) {
        Comments comments = new Comments();
        if (params != null && !params.isEmpty()) {
            comments = JSONObject.parseObject(params, Comments.class);
        }
        comments.setCid(id);
        comments.setParent(parent);
        comments.setAll(all);
        return comments;
    }

    // 辅助函数，根据id获取文章信息
    private Article getArticle(Integer id, Integer cid) {
        return contentsService.selectByKey(id != null ? id : cid);

    }

    // 辅助函数，从评论对象中提取数据
    private Map<String, Object> getDataFromComments(Comments _comments) {
        return JSONObject.parseObject(JSONObject.toJSONString(_comments));
    }

    // 辅助函数，从用户对象中提取数据
    private Map<String, Object> getDataFromUser(Users user) {
        Map<String, Object> dataUser;
        if (user.getUid() != null && !user.toString().isEmpty()) {
            dataUser = JSONObject.parseObject(JSONObject.toJSONString(user));
            dataUser.remove("password");
            if (user.getOpt() != null) {
                dataUser.put("opt", JSONObject.parseObject(user.getOpt()));
            }

        } else {
            dataUser = new HashMap<>();
            dataUser.put("screenName", "用户已注销");
            dataUser.put("avatar", null);
            dataUser.put("level", 0);
            dataUser.put("nextExp", 0);
            dataUser.put("isFollow", 0);
        }
        return dataUser;
    }

    // 辅助函数，检查用户是否点赞
    private Integer checkUserLike(Users user, Comments _comments) {
        Integer isLike = 0;
        if (user != null && !user.toString().isEmpty() && _comments != null) {
            CommentLike commentLike = new CommentLike();
            commentLike.setCid(_comments.getCid());
            commentLike.setUid(user.getUid());
            List<CommentLike> commentLikeList = commentlikeService.selectList(commentLike);
            if (!commentLikeList.isEmpty()) isLike = 1;
        }
        return isLike;
    }

    // 辅助函数，从评论对象中获取图片信息
    private List<String> getImagesFromComments(Comments _comments) {
        List<String> images;

        if (_comments.getImages() != null && !_comments.getImages().isEmpty()) {
            String imagesData = _comments.getImages();

            // 尝试解析为JSON数组
            try {
                JSONArray jsonArray = JSONArray.parseArray(imagesData);
                images = extractUrlsFromJsonArray(jsonArray);
            } catch (Exception e) {
                // 解析失败，则尝试提取单个URL
                images = extractUrlsFromString(imagesData);
            }
        } else {
            images = null;
        }

        return images;
    }

    // 从JSON数组中提取URL
    private List<String> extractUrlsFromJsonArray(JSONArray jsonArray) {
        List<String> urls = new ArrayList<>();
        for (Object obj : jsonArray) {
            String url = obj.toString();
            if (isValidUrl(url)) {
                urls.add(url);
            }
        }
        return urls;
    }

    // 从字符串中提取URL
    private List<String> extractUrlsFromString(String text) {
        List<String> urls = new ArrayList<>();
        Pattern pattern = Pattern.compile("https?://\\S+");
        Matcher matcher = pattern.matcher(text);
        while (matcher.find()) {
            String url = matcher.group();
            urls.add(url);
        }
        return urls;
    }

    // 检查URL的有效性
    private boolean isValidUrl(String url) {
        return url.startsWith("https://") || url.startsWith("http://");
    }    // 辅助函数，构建文章数据对象

    private Map<String, Object> getArticleData(Article article) {
        Map<String, Object> articleData = new HashMap<>();
        if (article == null || article.toString().isEmpty()) {
            articleData.put("title", "文章已删除");
            articleData.put("id", 0);
            articleData.put("authorId", 0);
        } else {
            articleData = JSONObject.parseObject(JSONObject.toJSONString(article));
            // 获取到图片
            List<String> images = baseFull.getImageSrc(article.getText());
            images = images.subList(0, Math.min(images.size(), 6)); // 获取前 6张图片
            articleData.remove("opt");
            articleData.put("image", !images.isEmpty() ? images.get(0) : new JSONArray());
            articleData.remove("text");

        }
        return articleData;
    }

    // 辅助函数，获取父评论信息
    private Map<String, Object> getParentCommentData(Comments _comments) {
        Map<String, Object> parentCommentData = new HashMap<>();
        if (_comments.getParent() != null && !_comments.getParent().equals(0) && !_comments.getParent().toString().isEmpty()) {
            Comments parentComment = service.selectByKey(_comments.getParent());
            Users parentUser = new Users();
            if (parentComment != null && !parentComment.toString().isEmpty()) {
                parentUser = usersService.selectByKey(parentComment.getUid());
            }
            Map<String, Object> dataParentUser = JSONObject.parseObject(JSONObject.toJSONString(parentUser));
            // 格式化opt
            if (parentUser != null && !dataParentUser.isEmpty()) {
                if (parentUser.getOpt() != null) {
                    dataParentUser.put("opt", JSONObject.parseObject(parentUser.getOpt()));
                }
                dataParentUser.remove("email");
                dataParentUser.remove("password");
                dataParentUser.remove("head_picture");
                dataParentUser.remove("rank");
                dataParentUser.remove("assets");
            }

            parentCommentData = JSONObject.parseObject(JSONObject.toJSONString(parentComment));
            parentCommentData.put("userInfo", dataParentUser);
        }
        return parentCommentData;
    }


    // 辅助函数，获取子评论信息列表
    private Map<String, Object> getSubDataList(Comments _comments, Article article) {
        Comments subComments = new Comments();
        subComments.setAll(_comments.getId());
        PageList<Comments> subCommentsPageList = service.selectPage(subComments, 1, 2, null, "created desc");
        List<Comments> subCommentsList = subCommentsPageList.getList();
        JSONArray subDataList = new JSONArray();
        Integer total = service.total(subComments, null);
        for (Comments _subComments : subCommentsList) {
            Map<String, Object> subData = JSONObject.parseObject(JSONObject.toJSONString(_subComments));
            Users subCommentUser = usersService.selectByKey(_subComments.getUid());
            if (subCommentUser == null) continue;
            Map<String, Object> subDataUser = JSONObject.parseObject(JSONObject.toJSONString(subCommentUser));
            subDataUser.remove("password");
            subDataUser.remove("email");
            subDataUser.remove("address");
            JSONObject opt = subCommentUser.getOpt() != null && !subCommentUser.getOpt().isEmpty() ? JSON.parseObject(subCommentUser.getOpt()) : new JSONObject();

            List images;

            Map<String, Object> subArticleData = new HashMap<>();
            if (article == null || article.toString().isEmpty()) {
                subArticleData.put("title", "文章已删除");
                subArticleData.put("id", 0);
                subArticleData.put("authorId", 0);
            } else {
                subArticleData = JSONObject.parseObject(JSONObject.toJSONString(article));
                images = article.getImages() != null ? JSONArray.parseArray(article.getImages()) : baseFull.getImageSrc(article.getText());
                subArticleData.put("images", images);
                subArticleData.remove("password");
                subArticleData.remove("opt");
                subArticleData.remove("isswiper");
            }
            subData.put("images", getImagesFromComments(_subComments));
            subData.put("userInfo", subDataUser);
            subData.put("article", subArticleData);
            subDataList.add(subData);
        }
        // 返回一个Map subDataList  和 tota
        Map<String, Object> data = new HashMap<>();
        data.put("subDataList", subDataList);
        data.put("total", subCommentsPageList.getTotalCount());
        return data;
    }

    // 辅助函数，将数据整合到主数据中
    private void assembleData(Map<String, Object> data, List images, Map<String, Object> articleData, Integer isLike, Map<String, Object> dataUser, Map<String, Object> parentCommentData, Map<String, Object> subMapData) {
        data.put("images", images);
        data.put("article", articleData);
        data.put("isLike", isLike);
        data.put("userInfo", dataUser);
        data.put("parentComment", parentCommentData);
        data.put("subComments", subMapData.get("subDataList"));
        data.put("subCommentCount", subMapData.get("total"));
    }


    /***
     * 添加评论
     */
    @RequestMapping(value = "/add")
    @XssCleanIgnore
    @ResponseBody
    public String add(@RequestParam(value = "id") Integer id,
                      @RequestParam(value = "parent", required = false, defaultValue = "0") Integer parent,
                      @RequestParam(value = "all", required = false, defaultValue = "0") Integer all,
                      @RequestParam(value = "text") String text,
                      @RequestParam(value = "images", required = false) String images,
                      HttpServletRequest request) {
        try {
            Apiconfig apiconfig = UStatus.getConfig(dataprefix, apiconfigService, redisTemplate);
            String token = request.getHeader("Authorization");
            Long timeStamp = System.currentTimeMillis() / 1000;
            Users user = new Users();
            if (token != null && !token.isEmpty()) {
                DecodedJWT verify = JWT.verify(token);
                user = usersService.selectByKey(Integer.parseInt(verify.getClaim("aud").asString()));
            }
            if (user.toString().isEmpty() || user.getUid() == null)
                return Result.getResultJson(201, "用户不存在,请重新登录", null);
            if (user.getBantime() != null && user.getBantime() > System.currentTimeMillis() / 1000) {
                return Result.getResultJson(201, "用户封禁中", null);
            }

            // 评论攻击拦截
            int commentCount = 0;
            if (redisTemplate.hasKey("commentTime_" + user.getUid())) {
                commentCount = Integer.parseInt(redisHelp.getRedis("commentTime_" + user.getUid(), redisTemplate));
            }
            if (commentCount > apiconfig.getCommentCount() && !apiconfig.getCommentCount().equals(0)) {
                return Result.getResultJson(201, "请等待1分钟后再评论", null);
            }
            redisHelp.delete("commentTime_" + user.getUid(), redisTemplate);
            redisHelp.setRedis("commentTime_" + user.getUid(), String.valueOf(commentCount + 1), 60, redisTemplate);

            // 定义一个变量来获取替换掉的内容
            String nonText = null;
            boolean permission = permission(token);

            if (text != null) {
                nonText = text.replaceAll("style=\"[^\"]*\"", ""); // 使用正则表达式匹配并替换带有 style 属性的部分
            }

            Article article = contentsService.selectByKey(id);
            Integer commentsNum = article.getCommentsNum() + 1;
            article.setCommentsNum(commentsNum);
            article.setReplyTime((int) System.currentTimeMillis() / 1000);
            contentsService.update(article);
            Users articleUser = usersService.selectByKey(article.getAuthorId());

            Inbox inbox = new Inbox();
            inbox.setText(article.getTitle());
            inbox.setTouid(article.getAuthorId());

            if (article == null || article.toString().isEmpty()) return Result.getResultJson(201, "帖子不存在", null);
            if (article.getStatus().equals("lock")) return Result.getResultJson(201, "帖子已被锁定！", null);
            Comments comments = new Comments();
            if (all != null && !all.toString().equals("")) comments.setAll(all);
            if (parent != null && !parent.toString().equals("") && !parent.equals(0)) {
                comments.setParent(parent);
                Comments parentComments = service.selectByKey(parent);
                if (parentComments != null && !parentComments.toString().isEmpty()) {
                    //查询父评论的用户
                    Users parentUser = usersService.selectByKey(parentComments.getUid());
                    inbox.setTouid(parentUser.getUid());
                    inbox.setText(nonText);
                    inbox.setValue(parentComments.getId());
                    // push发送
                    if (apiconfig.getIsPush().equals(1)) {
                        pushService.sendPushMsg(parentUser.getClientId(), "有新的评论", text, "payload", "system");
                    }
                }
            }

            if (text == null || text.isEmpty()) return Result.getResultJson(201, "请输入评论", null);
            comments.setText(user.getVip() < timeStamp && !permission ? nonText : text);
            if (images != null && !images.toString().isEmpty()) comments.setImages(images);
            comments.setIp(baseFull.getIpAddr(request));
            comments.setCreated(Math.toIntExact(timeStamp));
            comments.setCid(article.getCid());
            comments.setUid(user.getUid());
            comments.setType(0);

            service.insert(comments);
            // 给用户发消息
            inbox.setCreated(Math.toIntExact(timeStamp));
            inbox.setType("comment");
            inbox.setUid(user.getUid());
            if (parent == null || parent.equals(0)) {
                inbox.setValue(comments.getId());
            }
            inbox.setIsread(0);
            inboxService.insert(inbox);
            // push发送
            if (apiconfig.getIsPush().equals(1)) {
                pushService.sendPushMsg(articleUser.getClientId(), "有新的评论", text, "payload", "system");
            }
            // 评论奖励
            taskUtils.reward("comment", user, redisTemplate, usersService, taskService);
            return Result.getResultJson(200, "评论成功", null);
        } catch (Exception e) {
            e.printStackTrace();
            return Result.getResultJson(400, "接口异常", null);
        }

    }

    /***
     * 删除评论
     * @param id
     * @param request
     * @return
     */
    @RequestMapping(value = "/delete")
    @ResponseBody
    public String delete(@RequestParam(value = "id") Integer id, HttpServletRequest request) {
        try {
            String token = request.getHeader("Authorization");
            Boolean permission = permission(token);
            Users user = new Users();
            if (token != null && !token.isEmpty()) {
                DecodedJWT verify = JWT.verify(token);
                user = usersService.selectByKey(Integer.parseInt(verify.getClaim("aud").asString()));
            }
            Article article = contentsService.selectByKey(id);
            if (article == null) return Result.getResultJson(201, "帖子不存在", null);
            Comments comments = service.selectByKey(id);
            if (comments == null || comments.toString().isEmpty()) return Result.getResultJson(201, "评论不存在", null);
            if (!permission && user.getUid().equals(comments.getUid()))
                return Result.getResultJson(201, "无权限", null);

            service.delete(id);
            return Result.getResultJson(200, "删除成功", null);
        } catch (Exception e) {
            e.printStackTrace();
            return Result.getResultJson(400, "接口异常", null);
        }
    }

    /***
     *
     * @param id
     * @param text
     * @param request
     * @return
     */
    @RequestMapping(value = "/edit")
    @ResponseBody
    public String edit(@RequestParam(value = "id") Integer id, @RequestParam(value = "text") String text, HttpServletRequest request) {
        try {
            Boolean permission = permission(request.getHeader("Authorization"));
            String token = request.getHeader("Authorization");
            Users user = new Users();
            if (token != null && !token.isEmpty()) {
                DecodedJWT verify = JWT.verify(token);
                user = usersService.selectByKey(Integer.parseInt(verify.getClaim("aud").asString()));
            }
            Comments comments = service.selectByKey(id);
            if (comments == null || comments.toString().isEmpty()) return Result.getResultJson(201, "评论不存在", null);
            if (!permission || user.getUid().equals(comments.getUid()))
                return Result.getResultJson(201, "无权限", null);
            comments.setText(text);
            comments.setModified((int) (System.currentTimeMillis() / 1000));
            service.update(comments);
            return Result.getResultJson(200, "修改完成", null);
        } catch (Exception e) {
            e.printStackTrace();
            return Result.getResultJson(400, "接口异常", null);
        }
    }

    /***
     * 评论点赞
     * @param id
     * @param request
     * @return
     */
    @RequestMapping(value = "/like")
    @ResponseBody
    public String like(@RequestParam(value = "id") Integer id, HttpServletRequest request) {
        try {
            String token = request.getHeader("Authorization");
            Users user = new Users();
            if (token != null && !token.isEmpty()) {
                DecodedJWT verify = JWT.verify(token);
                user = usersService.selectByKey(Integer.parseInt(verify.getClaim("aud").asString()));
                if (user == null || user.toString().isEmpty()) return Result.getResultJson(201, "用户不存在", null);
            }
            Comments comments = service.selectByKey(id);
            if (comments == null || comments.toString().isEmpty()) return Result.getResultJson(201, "评论不存在", null);

            // 查询是否已经关注过了
            CommentLike commentLike = new CommentLike();
            commentLike.setCid(id);
            commentLike.setUid(user.getUid());
            List<CommentLike> commentLikeList = commentlikeService.selectList(commentLike);
            commentLike.setCreated((int) (System.currentTimeMillis() / 1000));

            // 获取评论
            Integer likes = comments.getLikes() == null ? 0 : comments.getLikes();
            if (commentLikeList != null && commentLikeList.size() > 0) {
                // 存在就删除
                commentlikeService.delete(commentLikeList.get(0).getId());
                comments.setLikes(likes > 0 ? likes - 1 : 0);
            } else {
                comments.setLikes(likes + 1);
                commentlikeService.insert(commentLike);
            }
            service.update(comments);
            return Result.getResultJson(200, commentLikeList.size() > 0 ? "已取消点赞" : "点赞成功", null);
        } catch (Exception e) {
            e.printStackTrace();
            return Result.getResultJson(400, "接口异常", null);
        }
    }

    @PostMapping(value = "/deleteUserComment")
    @ResponseBody
    public String deleteUserArticle(@RequestParam(value = "id") Integer id, HttpServletRequest request) {
        try {
            String token = request.getHeader("Authorization");
            if (!permission(token)) return Result.getResultJson(201, "无权限", null);

            service.deleteByAuthor(id);
            return Result.getResultJson(200, "已全部清除", null);

        } catch (Exception e) {
            e.printStackTrace();
            return Result.getResultJson(400, "接口异常", null);
        }
    }

    private boolean permission(String token) {
        if (token != null && !token.isEmpty()) {
            DecodedJWT verify = JWT.verify(token);
            Users user = usersService.selectByKey(Integer.parseInt(verify.getClaim("aud").asString()));
            if (user.getGroup().equals("administrator") || user.getGroup().equals("editor")) {
                return true;
            } else {
                return false;
            }
        } else {
            return false;
        }
    }
}
