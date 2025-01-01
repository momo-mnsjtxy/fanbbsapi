package com.Fanbbs.controller;

import com.Fanbbs.common.*;
import com.alibaba.fastjson.JSONArray;
import com.alibaba.fastjson.JSONObject;
import com.Fanbbs.entity.*;
import com.Fanbbs.service.*;
import com.auth0.jwt.interfaces.DecodedJWT;
import net.dreamlu.mica.xss.core.XssCleanIgnore;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.beans.factory.annotation.Value;
import org.springframework.data.redis.core.RedisTemplate;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.stereotype.Component;
import org.springframework.stereotype.Controller;
import org.springframework.web.bind.annotation.*;

import javax.servlet.http.HttpServletRequest;
import java.text.SimpleDateFormat;
import java.time.Duration;
import java.time.LocalDate;
import java.time.LocalDateTime;
import java.time.LocalTime;
import java.util.*;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/**
 * 控制层
 * ArticleController
 */
@Component
@Controller
@RequestMapping(value = "/article")
public class ArticleController {

    @Autowired
    ArticleService service;


    @Autowired
    private ShopService shopService;

    @Autowired
    private PaylogService paylogService;

    @Autowired
    private RelationshipsService relationshipsService;

    @Autowired
    private UserlogService userlogService;

    @Autowired
    private TaskService taskService;

    @Autowired
    private ArticleService articleService;

    @Autowired
    private CategoryService metasService;

    @Autowired
    private UsersService usersService;

    @Autowired
    private CommentsService commentsService;

    @Autowired
    private ApiconfigService apiconfigService;

    @Autowired
    private FanService fanService;

    @Autowired
    private PushService pushService;

    @Autowired
    private InboxService inboxService;

    @Autowired
    private RedisTemplate redisTemplate;

    @Value("${web.prefix}")
    private String dataprefix;

    @Value("${mybatis.configuration.variables.prefix}")
    private String prefix;


    @Autowired
    private JdbcTemplate jdbcTemplate;
    RedisHelp redisHelp = new RedisHelp();
    ResultAll Result = new ResultAll();
    baseFull baseFull = new baseFull();
    UserStatus UStatus = new UserStatus();

    TaskUtils taskUtils = new TaskUtils();

    /***
     * 文章详情
     */
    @GetMapping(value = "/info")
    @ResponseBody
    public String info(@RequestParam(value = "id") Integer id,
                       HttpServletRequest request) {
        try {
            Apiconfig apiconfig = UStatus.getConfig(dataprefix, apiconfigService, redisTemplate);
            String token = request.getHeader("Authorization");
            Users user = getUser(token);
            boolean permission = permission(user);
            int user_id = 0;
            if (user.getUid() != null) user_id = user.getUid();
            // 查询文章
            Article article = getArticle(id);
            if (article.getCid() == null) return Result.getResultJson(201, "数据不存在", null);
            article.setViews(article.getViews() + 1);
            service.update(article);

            //格式化数据
            JSONObject opt = article.getOpt() != null && !article.getOpt().isEmpty() ? JSONObject.parseObject(article.getOpt()) : new JSONObject();
            // 取出内容中的图片
            List<String> images = baseFull.getImageSrc(article.getText());
            if (article.getImages() != null && !article.getImages().isEmpty())
                images = JSONArray.parseArray(article.getImages(), String.class);

            // 用正则表达式匹配并替换[hide type=pay]这是付费查看的内容[/hide]，并根据type值替换成相应的提示
            Boolean isReply = hasComment(user, article);
            Boolean isPaid = hasPay(user, article);
            Boolean isLike = hasLike(user, article);
            Boolean isMark = hasMark(user, article);
            String text = hideText(isPaid, isReply, permission, article, user_id);
            List videos = getVideo(article.getVideos());
            Map<String, Object> category = getCategory(article.getMid());

            // 根据分类是否设置会员可见和用户是否是会员来决定内容是否可见
            Boolean showText = showText(user, article, category);
            Optional<JSONObject> objectOptional = Optional.ofNullable(opt)
                    .map(o -> o.getJSONArray("files"))
                    .filter(filesArray -> filesArray != null && !filesArray.isEmpty())
                    .map(filesArray -> JSONObject.parseObject(filesArray.get(0).toString()));
            JSONObject object = new JSONObject();
            if (objectOptional.isPresent()) {
                object = objectOptional.get();
            }

            if (!showText && !permission) {
                images = images.subList(0, Math.min(images.size(), 10)); // 获取前 10 张图片
                if (!article.getType().equals("video")) {
                    text = "";
                } else {
                    videos = new ArrayList<>();
                }

            }

            // 是否登录查看
            if (apiconfig.getIsLogin().equals(1) && user_id == 0) {
                text = "<div style=\"border-radius:5px;border:#fb7299 1px solid;padding:15px;text-align:center\">\n" +
                        " \t<span style=\"color:#fb7299\">隐藏内容登录后查看</span>\n" +
                        "</div>";
            }

            List tagDataList = getTags(article);
            // 获取作者信息
            Map<String, Object> authorInfo = getAuthorInfo(user_id, article);
            // 返回信息
            Map<String, Object> data = JSONObject.parseObject(JSONObject.toJSONString(article), Map.class);
            // 加入信息
            data.put("images", images);
            data.put("videos", videos);
            data.put("text", text);
            data.put("category", category);
            data.put("tag", tagDataList);
            data.put("isLike", isLike);
            data.put("isMark", isMark);
            data.put("authorInfo", authorInfo);
            data.put("showText", showText);
            data.put("isHide", false);
            data.put("opt", opt);
            // 移除信息
            data.remove("passowrd");
            // 判断是是否是隐藏内容

            if ((!article.getPrice().equals(0) && !article.getAuthorId().equals(user_id) && !permission && !isPaid)) {
                data.put("opt", new Object());
                data.put("isHide", true);
            }

            if (apiconfig.getIsLogin().equals(1) && user_id == 0) {
                data.put("text", "请登陆后查看");
                data.put("opt", new Object());
                data.put("isHide", false);
            }
            if (!showText && !permission) {
                data.put("opt", new Object());
                data.put("isHide", true);
            }
            // 添加访问经验
            if (user.getUid() != null) taskUtils.reward("view", user, redisTemplate, usersService, taskService);
            return Result.getResultJson(200, "获取成功", data);
        } catch (Exception e) {
            e.printStackTrace();
            return Result.getResultJson(400, "接口错误", null);
        }
    }


    /***
     * 文章列表
     */
    @GetMapping(value = "/articleList")
    @ResponseBody
    public String articleList(@RequestParam(value = "page", required = false, defaultValue = "1") Integer page,
                              @RequestParam(value = "limit", required = false, defaultValue = "10") Integer limit,
                              @RequestParam(value = "params", required = false) String params,
                              @RequestParam(value = "random", required = false, defaultValue = "0") Integer random,
                              @RequestParam(value = "searchKey", required = false) String searchKey,
                              @RequestParam(value = "tag", required = false) Integer tagId,
                              @RequestParam(value = "order", required = false, defaultValue = "created desc") String order,
                              HttpServletRequest request) {
        try {
            String token = request.getHeader("Authorization");
            Users user = getUser(token);
            boolean permission = permission(user);
            int user_id = 0;
            if (user.getUid() != null) user_id = user.getUid();
            Article query = new Article();
            if (params != null && !params.isEmpty()) {
                query = JSONObject.parseObject(params, Article.class);
            }
            query.setStatus(permission ? null : "publish");
            PageList<Article> articlePageList = service.selectPage(query, page, limit, searchKey, order, random, tagId);
            List<Article> articleList = articlePageList.getList();
            List dataList = new ArrayList<>();
            for (Article article : articleList) {
                Map<String, Object> data = JSONObject.parseObject(JSONObject.toJSONString(article), Map.class);
                if (article == null || article.getCid() == null) continue;
                // 取出内容中的图片
                List<String> images = baseFull.getImageSrc(article.getText());
                if (article.getImages() != null && !article.getImages().isEmpty())
                    images = JSONArray.parseArray(article.getImages(), String.class);
                int images_size = images.size();
                // 用正则表达式匹配并替换[hide type=pay]这是付费查看的内容[/hide]，并根据type值替换成相应的提示
                images = images.subList(0, Math.min(images.size(), 10)); // 获取前 10 张图片
                Boolean isReply = hasComment(user, article);
                Boolean isPaid = hasPay(user, article);
                Boolean isLike = hasLike(user, article);
                Boolean isMark = hasMark(user, article);
                String text = baseFull.toStrByChinese(hideText(isPaid, isReply, permission, article, user_id));
                List videos = getVideo(article.getVideos());
                if (article.getType().equals("video")) images = getPoster(videos);

                Map<String, Object> category = getCategory(article.getMid());
                // 根据分类是否设置会员可见和用户是否是会员来决定内容是否可见
                Boolean showText = showText(user, article, category);
                List tagDataList = getTags(article);
                // 获取作者信息
                Map<String, Object> authorInfo = getAuthorInfo(user_id, article);
                if (authorInfo == null) continue;

                // 加入信息
                data.put("images_size", images_size);
                data.put("images", images);
                data.put("videos", videos);
                data.put("text", text);
                data.put("category", category);
                data.put("tag", tagDataList);
                data.put("isLike", isLike);
                data.put("isMark", isMark);
                data.put("authorInfo", authorInfo);
                data.put("showText", showText);
                // 移除信息
                data.remove("passowrd");
                data.remove("hotScore");
                data.remove("opt");
                data.remove("videos");
                dataList.add(data);
            }
            // 返回信息
            Map<String, Object> data = new HashMap<>();
            data.put("page", page);
            data.put("limit", limit);
            data.put("data", dataList);
            data.put("total", service.total(query, searchKey));
            data.put("count", articleList.size());
            return Result.getResultJson(200, "获取成功", data);
        } catch (Exception e) {
            e.printStackTrace();
            return Result.getResultJson(400, "接口异常", null);
        }

    }


    /***
     * 发布文章
     */
    @PostMapping(value = "/articleAdd")
    @ResponseBody
    @XssCleanIgnore
    public String articleAdd(@RequestParam(value = "title") String title,
                             @RequestParam(value = "text") String text,
                             @RequestParam(value = "category") Integer category,
                             @RequestParam(value = "tag", required = false) String tag,
                             @RequestParam(value = "newTag", required = false) String newTag,
                             @RequestParam(value = "type", required = false, defaultValue = "post") String type,
                             @RequestParam(value = "videos", required = false) String videos,
                             @RequestParam(value = "opt", required = false) String opt,
                             @RequestParam(value = "price", required = false, defaultValue = "0") Integer price,
                             @RequestParam(value = "discount", required = false, defaultValue = "1") Float discount,
                             HttpServletRequest request) {
        try {
            Apiconfig apiconfig = UStatus.getConfig(this.dataprefix, apiconfigService, redisTemplate);
            String token = request.getHeader("Authorization");
            Users user = getUser(token);
            boolean permission = permission(user);
            if (user == null || user.getUid() == null) return Result.getResultJson(201, "用户不存在，请重新登录", null);
            if (user.getBantime() != null && user.getBantime() > System.currentTimeMillis() / 1000) {
                return Result.getResultJson(201, "用户封禁中", null);
            }

            // 判断等级
            int level = baseFull.getLevel(user.getExperience(), dataprefix, apiconfigService, redisTemplate).get(0);
            if (!apiconfig.getPublishLevel().equals(0) && level < apiconfig.getPublishLevel() && !permission)
                return Result.getResultJson(201, "您的等级尚未达到发帖需求：等级" + apiconfig.getPublishLevel(), null);

            // 发布太频繁就禁止
            if (Boolean.TRUE.equals(redisTemplate.hasKey("articleAdd_" + user.getUid())))
                return Result.getResultJson(201, "请等待" + apiconfig.getPublishInterval() + "秒后再发布", null);

            // 判断
            if (type.equals("video") && videos.isEmpty())
                return Result.getResultJson(201, "请上传视频和封面", null);

            if (title == null || title.length() < 3) {
                return Result.getResultJson(201, "标题太短", null);
            }
            if (text == null || text.length() < 10) {
                return Result.getResultJson(201, "内容太少", null);
            }
            if (category == null) {
                return Result.getResultJson(201, "请选择分类", null);
            }

            // 查询分类是否存在
            Category _category = metasService.selectByKey(category);
            if (_category == null || _category.toString().isEmpty()) {
                return Result.getResultJson(201, "分类不存在", null);
            }

            if (_category.getPermission().equals(1) && !permission)
                return Result.getResultJson(201, "仅限管理员发布", null);
            if (_category.getIsvip().equals(1) && user.getVip() < System.currentTimeMillis() / 1000 && !permission)
                return Result.getResultJson(201, "仅限会员发布", null);


            // 写入文章信息
            Article article = new Article();
            article.setStatus("publish");
            article.setAuthorId(user.getUid());
            article.setText(text);
            article.setTitle(title);
            article.setMid(category);
            article.setType(type);
            article.setPrice(_category.getIsvip().equals(0) ? price : 0); // 会员分类 设置价格无效
            article.setDiscount(discount);
            article.setOpt(opt);
            article.setVideos(videos);
            article.setCreated(Integer.parseInt(String.valueOf(System.currentTimeMillis() / 1000)));
            if (apiconfig.getContentAuditlevel().equals(1) && !permission) article.setStatus("waiting");
            if (apiconfig.getContentAuditlevel().equals(2)) article.setStatus("waiting");
            // 写入Tag和分类
            service.insert(article);
            _category.setCount(_category.getCount() + 1);
            metasService.update(_category);
            Relationships related = new Relationships();
            related.setCid(article.getCid());
            related.setMid(category);
            relationshipsService.insert(related);

            //写入Tag 将字符串分出来
            if (tag != null && !tag.isEmpty()) {
                String[] tags = tag.split(",");
                for (String relateTag : tags) {
                    related.setMid(Integer.parseInt(relateTag));
                    relationshipsService.insert(related);
                }
            }
            // 分出新的tag
            if (newTag != null && !newTag.isEmpty()) {
                String[] tags = newTag.split(",");
                for (String relateTag : tags) {
                    // 查询tag是否存在，不存在就添加，如果存在就插入记录//
                    Category _newTag = new Category();
                    _newTag.setName(relateTag);
                    List<Category> tagList = metasService.selectList(_newTag);
                    if (!tagList.isEmpty()) {
                        // 存在就插入
                        related.setMid(tagList.get(0).getMid());
                        relationshipsService.insert(related);
                    } else {
                        // 新建一个tag并插入
                        _newTag.setType("tag");
                        metasService.insert(_newTag);
                        related.setMid(_newTag.getMid());
                        relationshipsService.insert(related);
                    }

                }
            }
            // 发布奖励
            if (article.getStatus().equals("publish"))
                taskUtils.reward("publish", user, redisTemplate, usersService, taskService);
            if (!apiconfig.getPublishInterval().equals(0)) {
                redisHelp.setRedis("articleAdd_" + user.getUid(), null, apiconfig.getPublishInterval(), redisTemplate);

            }
            return Result.getResultJson(200, article.getStatus().equals("publish") ? "发布成功" : "发布成功，请等待审核", null);
        } catch (Exception e) {
            e.printStackTrace();
            return Result.getResultJson(400, "接口异常", null);
        }

    }


    /***
     * 文章更新
     */

    @PostMapping(value = "/update")
    @ResponseBody
    @XssCleanIgnore
    public String update(@RequestParam(value = "id") Integer id,
                         @RequestParam(value = "title") String title,
                         @RequestParam(value = "text") String text,
                         @RequestParam(value = "category") Integer category,
                         @RequestParam(value = "tag", required = false) String tag,
                         @RequestParam(value = "opt", required = false) String opt,
                         @RequestParam(value = "videos", required = false) String videos,
                         @RequestParam(value = "price", required = false, defaultValue = "0") Integer price,
                         @RequestParam(value = "discount", required = false, defaultValue = "1") Float discount,
                         HttpServletRequest request) {

        try {
            String token = request.getHeader("Authorization");
            Users user = getUser(token);
            Boolean permission = permission(user);
            int user_id = 0;
            if (user.getUid() != null) user_id = user.getUid();
            Apiconfig apiconfig = UStatus.getConfig(this.dataprefix, apiconfigService, redisTemplate);
            Article article = service.selectByKey(id);
            if (!permission && !article.getAuthorId().equals(user_id)) return Result.getResultJson(201, "无权限", null);
            if (user != null && user.getBantime() > System.currentTimeMillis() / 1000)
                return Result.getResultJson(201, "用户封禁中", null);
            if (article.getStatus().equals("lock")) return Result.getResultJson(201, "帖子已被锁定！", null);
            if (article.getType().equals("video") && videos.isEmpty())
                return Result.getResultJson(201, "请上传视频和封面", null);
            // 更新分类
            relationshipsService.delete(article.getCid());
            Relationships relate = new Relationships();
            relate.setMid(category);
            relate.setCid(article.getCid());
            relationshipsService.insert(relate);
            // 重新设置tag
            if (tag != null && !tag.isEmpty()) {
                String[] tagList = tag.split(",");
                for (String tags : tagList) {
                    relate.setMid(Integer.parseInt(tags));
                    relationshipsService.insert(relate);
                }
            }
            // 设置文章信息
            article.setMid(category);
            article.setText(text);
            article.setTitle(title);
            article.setOpt(opt);
            article.setPrice(price);
            article.setVideos(videos);
            article.setDiscount(discount);
            article.setModified((int) (System.currentTimeMillis() / 1000));
            if (apiconfig.getContentAuditlevel().equals(1)) article.setStatus("waiting");
            if (apiconfig.getContentAuditlevel().equals(2)) {
                if (!permission && article.getStatus().equals("waiting")) article.setStatus("waiting");
            }
            service.update(article);
            return Result.getResultJson(200, "更新成功", null);
        } catch (Exception e) {
            e.printStackTrace();
            return Result.getResultJson(400, "接口异常", null);
        }

    }


    @PostMapping(value = "/delete")
    @ResponseBody
    public String delete(@RequestParam(value = "id") Integer id,
                         HttpServletRequest request) {
        try {
            String token = request.getHeader("Authorization");
            Users user = getUser(token);
            Boolean permission = permission(user);
            int user_id = 0;
            if (user.getUid() != null) user_id = user.getUid();
            Article article = service.selectByKey(id);
            if (!permission && !article.getAuthorId().equals(user_id)) return Result.getResultJson(201, "无权限", null);
            Apiconfig apiconfig = UStatus.getConfig(dataprefix, apiconfigService, redisTemplate);
            Users author = usersService.selectByKey(article.getAuthorId());

            if (article.getStatus().equals("lock") && !permission)
                return Result.getResultJson(201, "帖子已被锁定！", null);
            // 删除
            service.delete(id);
            // 处理链表
            relationshipsService.delete(article.getCid());

            return Result.getResultJson(200, "删除成功", null);
        } catch (Exception e) {
            e.printStackTrace();
            return Result.getResultJson(400, "接口异常", null);
        }
    }

    /***
     * 文章审核
     */
    @PostMapping(value = "/audit")
    @ResponseBody
    public String audit(@RequestParam(value = "id") Integer id,
                        @RequestParam(value = "type") Integer type,
                        @RequestParam(value = "text") String text,
                        HttpServletRequest request) {
        try {
            String token = request.getHeader("Authorization");
            Users user = getUser(token);
            if (!permission(user)) return Result.getResultJson(201, "无权限", null);

            Apiconfig apiconfig = UStatus.getConfig(dataprefix, apiconfigService, redisTemplate);
            Article article = service.selectByKey(id);
            Users author = usersService.selectByKey(article.getAuthorId());
            Inbox inbox = new Inbox();
            inbox.setTouid(article.getAuthorId());
            inbox.setValue(article.getCid());
            inbox.setType("system");
            if (type.equals(0)) {
                article.setStatus("reject");
                inbox.setText("你的文章[" + article.getTitle() + "]审核不通过;原因" + text);
            }
            if (type.equals(1)) {
                article.setStatus("publish");
                inbox.setText("你的文章[" + article.getTitle() + "]审核已通过！");
            }
            if (apiconfig.getIsPush().equals(1)) {
                try {
                    pushService.sendPushMsg(author.getClientId(), "审核通知", "文章[" + article.getTitle() + "]" + (type.equals(0) ? "审核不通过" : "审核通过"), "payload", article.getCid().toString());
                } catch (Exception e) {
                    e.printStackTrace();
                }
            }
            service.update(article);
            inboxService.insert(inbox);
            // 添加经验
            return Result.getResultJson(200, "操作成功", null);
        } catch (Exception e) {
            e.printStackTrace();
            return Result.getResultJson(400, "接口异常", null);
        }
    }


    /***
     * 文章操作
     */
    @PostMapping(value = "/action")
    @ResponseBody
    public String action(@RequestParam(value = "id") int id,
                         @RequestParam(value = "type") String type,
                         HttpServletRequest request) {
        try {
            String token = request.getHeader("Authorization");
            Users user = getUser(token);
            if (!permission(user)) return Result.getResultJson(201, "无权限", null);
            Article article = service.selectByKey(id);
            switch (type) {
                case "recommend":
                    article.setIsrecommend(article.getIsrecommend() > 0 ? 0 : 1);
                    break;
                case "top":
                    article.setIstop(article.getIstop() > 0 ? 0 : 1);
                    break;
                case "swiper":
                    article.setIsswiper(article.getIsswiper() > 0 ? 0 : 1);
                    break;
                case "circleTop":
                    article.setIsCircleTop(article.getIsCircleTop() > 0 ? 0 : 1);
                    break;
                case "publish":
                    article.setStatus(article.getStatus().equals("waiting") ? "publish" : "waiting");

                    String redisKey = dataprefix + "_articlePublish_" + article.getCid();
                    Boolean isPublished = redisTemplate.hasKey(redisKey);
                    if (!isPublished) {
                        taskUtils.reward("publish", usersService.selectByKey(article.getAuthorId()), redisTemplate, usersService, taskService);
                        redisTemplate.opsForValue().set(redisKey, "publish", Duration.ofDays(365));
                    }
                    break;
            }
            service.update(article);
            Map<String, Object> data = new HashMap<>();
            data.put("type", type);
            return Result.getResultJson(200, "操作成功", data);
        } catch (Exception e) {
            e.printStackTrace();
            return Result.getResultJson(400, "接口异常", null);
        }
    }

    /**
     * 购买文章隐藏内容
     */
    @PostMapping(value = "/buy")
    @ResponseBody
    public String buy(HttpServletRequest request,
                      @RequestParam(value = "id") int id) {
        try {
            String token = request.getHeader("Authorization");
            Users user = getUser(token);
            long timeStamp = System.currentTimeMillis() / 1000;
            boolean vip = false;
            if (user.getUid() != null && user.getVip() > System.currentTimeMillis() / 100) vip = true;
            Apiconfig apiconfig = UStatus.getConfig(dataprefix, apiconfigService, redisTemplate);
            // 文章信息
            Article article = service.selectByKey(id);
            if (article.getAuthorId().equals(user.getUid())) {
                return Result.getResultJson(201, "不可购买自己的文章", null);
            }
            // 查询是否已经购买过
            Paylog buyStatus = new Paylog();
            buyStatus.setUid(user.getUid());
            buyStatus.setCid(id);
            int isBuy = paylogService.total(buyStatus);
            if (isBuy > 0) {
                return Result.getResultJson(201, "无需重复购买", null);
            }
            // 开始判断余额
            if (user.getAssets() == null || user.getAssets() < article.getPrice()) {
                return Result.getResultJson(201, "余额不足", null);
            }

            // 购买 减除购买者的资产
            user.setAssets(user.getAssets() - article.getPrice());
            int price = article.getPrice(); // 获取文章原价
            if (vip && article.getDiscount() < 1) {
                price = (int) (price * article.getDiscount()); // 计算折扣后的价格
            } else if (vip) {
                price = (int) (price * Float.parseFloat(apiconfig.getVipDiscount())); // 计算 VIP 折扣后的价格
            }

            user.setAssets(user.getAssets() - price); // 减去购买价格
            //生成订单号
            // 将时间戳转换成日期对象
            Date date = new Date(timeStamp * 1000L);
            SimpleDateFormat sdf = new SimpleDateFormat("yyMMdd");
            String order = sdf.format(date) + timeStamp + user.getUid();
            Paylog pay = new Paylog();
            pay.setSubject("查看付费文章[" + article.getTitle() + "]");
            pay.setTotalAmount(String.valueOf(price * -1));
            pay.setStatus(1);
            pay.setPaytype("article");
            pay.setUid(user.getUid());
            pay.setCid(article.getCid());
            pay.setOutTradeNo(order);
            paylogService.insert(pay);
            usersService.update(user);

            //给作者写入站内信息
            Inbox inbox = new Inbox();
            inbox.setText("出售文章[" + article.getTitle() + "],获得" + Math.floor(price * 0.5) + "积分");
            inbox.setValue(article.getCid());
            inbox.setTouid(article.getAuthorId());
            inbox.setType("finance");
            inbox.setCreated(Math.toIntExact(timeStamp));
            inboxService.insert(inbox);

            // 写入作者获取积分记录
            pay.setSubject("出售文章[" + article.getTitle() + "]");
            pay.setTotalAmount(String.valueOf(Math.floor(price * 0.5)));
            pay.setUid(article.getAuthorId());
            paylogService.insert(pay);

            // 更新作者资产
            Users articleUser = usersService.selectByKey(article.getAuthorId());
            articleUser.setAssets(articleUser.getAssets() + (int) Math.round(0.5 * price));
            usersService.update(articleUser);

            return Result.getResultJson(200, "购买成功", null);
        } catch (Exception e) {
            e.printStackTrace();
            return Result.getResultJson(400, "接口异常", null);
        }

    }


    @GetMapping(value = "/rewardList")
    @ResponseBody
    public String rewardList(@RequestParam(value = "page", required = false, defaultValue = "1") int page,
                             @RequestParam(value = "limit", required = false, defaultValue = "10") int limit,
                             @RequestParam(value = "id") int id) {
        try {
            // 查询文章是否存在
            Article article = service.selectByKey(id);
            if (article == null || article.toString().isEmpty()) return Result.getResultJson(201, "文章不存在", null);
            // 获取投喂人
            Userlog userlog = new Userlog();
            userlog.setType("reward");
            userlog.setCid(article.getCid());

            PageList<Userlog> userlogPageList = userlogService.selectPage(userlog, page, limit);
            List<Userlog> userlogList = userlogPageList.getList();
            List<Object> dataList = new ArrayList<>();
            for (Userlog _userlog : userlogList) {
                Map<String, Object> data = JSONObject.parseObject(JSONObject.toJSONString(usersService.selectByKey(_userlog.getUid())));
                //移除铭感信息
                if (data != null) {
                    data.remove("mail");
                    data.remove("password");
                    data.remove("assets");
                    data.remove("opt");
                } else {
                    data.put("screenName", "用户已注销");
                    data.put("uid", 0);
                }
                dataList.add(data);
            }

            Map<String, Object> data = new HashMap<>();
            data.put("data", dataList);
            data.put("count", dataList.size());

            return Result.getResultJson(200, "获取成功", data);

        } catch (Exception e) {
            e.printStackTrace();
            return Result.getResultJson(400, "接口异常", null);
        }
    }

    @PostMapping(value = "/reward")
    @ResponseBody
    public String reward(@RequestParam(value = "id") int id,
                         @RequestParam(value = "money") int money,
                         HttpServletRequest request) {
        try {
            String token = request.getHeader("Authorization");
            Users user = getUser(token);
            if (money < 2) return Result.getResultJson(201, "最低打赏2积分", null);
            if (user == null || user.getUid() == null) return Result.getResultJson(201, "用户不存在，请重新登录", null);
            if (user.getAssets() < money) return Result.getResultJson(201, "积分不足", null);
            Article article = service.selectByKey(id);
            if (article == null || article.getCid() == null) return Result.getResultJson(201, "文章不存在", null);
            Users author = usersService.selectByKey(article.getAuthorId());

            // 如果已经赞赏了就不继续执行
            Userlog haslog = new Userlog();
            haslog.setUid(user.getUid());
            haslog.setCid(article.getCid());
            haslog.setType("reward");
            if (!userlogService.selectList(haslog).isEmpty()) return Result.getResultJson(201, "你已经打赏过了", null);

            Integer timeStamp = Math.toIntExact(System.currentTimeMillis() / 1000);
            long round = Math.round(Math.abs(money * 0.5));

            // 给文章作者加积分
            Paylog paylog = new Paylog();
            paylog.setUid(article.getAuthorId());
            paylog.setSubject("获得文章打赏");
            paylog.setTotalAmount(String.valueOf(round));
            paylog.setStatus(1);
            paylog.setPaytype("reward");
            paylog.setCreated(timeStamp);
            author.setAssets((int) (author.getAssets() + round));
            paylogService.insert(paylog);
            usersService.update(author);

            // 扣除打赏者的积分
            paylog.setUid(user.getUid());
            paylog.setSubject("打赏文章扣除");
            paylog.setTotalAmount(String.valueOf(money * -1));
            paylog.setStatus(1);
            paylog.setPaytype("reward");
            paylog.setCreated(timeStamp);
            user.setAssets(user.getAssets() - money);
            paylogService.insert(paylog);
            usersService.update(user);

            // 写入日志
            Userlog userlog = new Userlog();
            userlog.setUid(user.getUid());
            userlog.setCid(article.getCid());
            userlog.setType("reward");
            userlog.setNum(money);
            userlog.setCreated(timeStamp);
            userlogService.insert(userlog);
            // 更新article
            article.setRewards(article.getRewards() + money);
            articleService.update(article);
            return Result.getResultJson(200, "打赏成功", null);

        } catch (Exception e) {
            e.printStackTrace();
            return Result.getResultJson(400, "接口异常", null);
        }
    }

    /***
     * 全站统计
     */
    @GetMapping(value = "/allData")
    @ResponseBody
    public String allData(HttpServletRequest request) {
        String token = request.getHeader("Authorization");
        Users user = getUser(token);
        if (!permission(user)) return Result.getResultJson(201, "无权限", null);

        JSONObject data = new JSONObject();
        Article contents = new Article();
        contents.setStatus("publish");
        Integer allContents = service.total(contents, null);

        Comments comments = new Comments();
        Integer allComments = commentsService.total(comments, null);

        Users users = new Users();
        Integer allUsers = usersService.total(users, null);


        Shop shop = new Shop();
        Integer allShop = shopService.total(shop, null);


        contents.setType("post");
        contents.setStatus("waiting");
        Integer upcomingContents = service.total(contents, null);

        Integer upcomingComments = commentsService.total(comments, null);

        shop.setStatus(0);
        Integer upcomingShop = shopService.total(shop, null);


        Userlog userlog = new Userlog();
        userlog.setType("withdraw");
        userlog.setCid(-1);
        Integer upcomingWithdraw = userlogService.total(userlog);


        data.put("allContents", allContents);
        data.put("allComments", allComments);
        data.put("allUsers", allUsers);
        data.put("allShop", allShop);
        data.put("upcomingContents", upcomingContents);
        data.put("upcomingComments", upcomingComments);
        data.put("upcomingShop", upcomingShop);
        data.put("upcomingWithdraw", upcomingWithdraw);

        return Result.getResultJson(200, "获取成功", data);
    }

    /***
     * 关注用户的文章
     */
    @GetMapping(value = "/follow")
    @ResponseBody
    public String follow(@RequestParam(value = "page", required = false, defaultValue = "1") Integer page,
                         @RequestParam(value = "limit", required = false, defaultValue = "10") Integer limit,
                         @RequestParam(value = "order", required = false, defaultValue = "created desc") String order,
                         HttpServletRequest request) {
        try {
            String token = request.getHeader("Authorization");
            Users user = getUser(token);
            int user_id = 0;
            if (user.getUid() != null) user_id = user.getUid();
            Boolean permission = permission(user);
            int offset = (page - 1) * limit; // 计算偏移量
            String sql = "SELECT content.* FROM " + prefix + "_contents AS content JOIN " + prefix + "_fan AS fan ON content.authorId = fan.touid WHERE fan.uid = ? AND content.status = 'publish' ORDER BY content.created DESC LIMIT ?, ?";
            List<Map<String, Object>> articleList = jdbcTemplate.queryForList(sql, user.getUid().toString(), offset, limit);
            JSONArray dataList = new JSONArray();
            for (Map<String, Object> article : articleList) {
                Article articleData = JSONObject.parseObject(JSONObject.toJSONString(article), Article.class);
                Map<String, Object> data = JSONObject.parseObject(JSONObject.toJSONString(article), Map.class);
                //格式化数据
                JSONObject opt = articleData.getOpt() != null && !articleData.getOpt().isEmpty() ? JSONObject.parseObject(articleData.getOpt()) : null;
                // 取出内容中的图片
                List<String> images = baseFull.getImageSrc(articleData.getText());
                // 用正则表达式匹配并替换[hide type=pay]这是付费查看的内容[/hide]，并根据type值替换成相应的提示
                Boolean isReply = hasComment(user, articleData);
                Boolean isPaid = hasPay(user, articleData);
                Boolean isLike = hasLike(user, articleData);
                Boolean isMark = hasMark(user, articleData);
                String text = baseFull.toStrByChinese(hideText(isPaid, isReply, permission, articleData, user_id));
                Map<String, Object> category = getCategory(articleData.getMid());
                // 根据分类是否设置会员可见和用户是否是会员来决定内容是否可见
                Boolean showText = showText(user, articleData, category);
                if (!showText && !permission) text = "";
                // 标签
                Relationships tagQuery = new Relationships();
                tagQuery.setCid(articleData.getCid());
                List<Relationships> tagList = relationshipsService.selectList(tagQuery);
                JSONArray tagDataList = new JSONArray();
                for (Relationships tag : tagList) {
                    Category tagsQuery = new Category();
                    tagsQuery.setMid(tag.getMid());
                    tagsQuery.setType("tag");
                    List<Category> tagInfo = metasService.selectList(tagsQuery);
                    if (tagInfo.size() > 0) {
                        Map<String, Object> tagData = JSONObject.parseObject(JSONObject.toJSONString(tagInfo.get(0)), Map.class);
                        // 移除信息
                        tagData.remove("opt");
                        tagDataList.add(tagData);
                    }
                }
                // 获取作者信息
                Map<String, Object> authorInfo = getAuthorInfo(user_id, articleData);
                // 加入信息
                if (articleData.getImages() != null && !articleData.getImages().isEmpty())
                    data.put("images", JSONArray.parseArray(articleData.getImages()));
                else data.put("images", images);
                data.put("opt", opt);
                data.put("text", text);
                data.put("category", category);
                data.put("tag", tagDataList);
                data.put("isLike", isLike);
                data.put("isMark", isMark);
                data.put("authorInfo", authorInfo);
                data.put("showText", showText);
                // 移除信息
                data.remove("passowrd");
                Optional<JSONObject> objectOptional = Optional.ofNullable(opt)
                        .map(o -> o.getJSONArray("files"))
                        .filter(filesArray -> filesArray != null && filesArray.size() > 0)
                        .map(filesArray -> JSONObject.parseObject(filesArray.get(0).toString()));
                JSONObject object = new JSONObject();
                if (objectOptional.isPresent()) {
                    object = objectOptional.get();
                }
                // 判断是是否是隐藏内容
                if (!articleData.getAuthorId().equals(user_id) && !isPaid && articleData.getPrice() != 0 && object != null && object.containsKey("link") && !permission) {
                    data.put("opt", null);
                    data.put("isHide", 1);
                } else {
                    data.put("isHide", 0);
                }
                dataList.add(data);
            }
            Map<String, Object> data = new HashMap<>();
            data.put("page", page);
            data.put("limit", limit);
            data.put("data", dataList);
            data.put("count", articleList.size());
            return Result.getResultJson(200, "获取成功", data);

        } catch (Exception e) {
            e.printStackTrace();
            return Result.getResultJson(400, "接口异常", null);
        }
    }

    /***
     * 点赞
     * @param id
     * @param request
     * @return
     */

    @PostMapping(value = "/like")
    @ResponseBody
    public String like(@RequestParam(value = "id") Integer id,
                       HttpServletRequest request) {
        try {
            String token = request.getHeader("Authorization");
            Users user = getUser(token);
            if (user.getUid() == null || user.toString().isEmpty())
                return Result.getResultJson(201, "用户不存在", null);

            Article article = service.selectByKey(id);
            if (article == null || article.toString().isEmpty()) return Result.getResultJson(201, "文章不存在", null);

            //查询是否点过赞
            Userlog userlog = new Userlog();
            userlog.setType("articleLike");
            userlog.setCid(article.getCid());
            userlog.setUid(user.getUid());
            Integer likes = article.getLikes();

            // 存在就删除
            List<Userlog> userlogList = userlogService.selectList(userlog);
            if (!userlogList.isEmpty()) {
                article.setLikes(likes > 0 ? likes - 1 : 0);
                userlogService.delete(userlogList.get(0).getId());
            } else {
                userlog.setCreated((int) (System.currentTimeMillis() / 1000));
                article.setLikes(likes + 1);
                userlogService.insert(userlog);
                // 点赞奖励
                taskUtils.reward("like", user, redisTemplate, usersService, taskService);
            }
            service.update(article);
            return Result.getResultJson(200, !userlogList.isEmpty() ? "已取消点赞" : "点赞成功", null);

        } catch (Exception e) {
            e.printStackTrace();
            return Result.getResultJson(400, "接口异常", null);
        }
    }

    @PostMapping(value = "/mark")
    @ResponseBody
    public String mark(@RequestParam(value = "id") Integer id,
                       HttpServletRequest request) {
        try {
            String token = request.getHeader("Authorization");
            Users user = getUser(token);
            if (user.getUid() == null || user.toString().isEmpty())
                return Result.getResultJson(201, "用户不存在", null);

            Article article = service.selectByKey(id);
            if (article == null || article.toString().isEmpty()) return Result.getResultJson(201, "文章不存在", null);

            //查询是否收藏
            Userlog userlog = new Userlog();
            userlog.setType("articleMark");
            userlog.setCid(article.getCid());
            userlog.setUid(user.getUid());
            // 存在就删除
            List<Userlog> userlogList = userlogService.selectList(userlog);
            if (userlogList.size() > 0) {
                article.setMarks(article.getMarks() > 0 ? article.getMarks() - 1 : 0);
                userlogService.delete(userlogList.get(0).getId());
            } else {
                article.setMarks(article.getMarks() + 1);
                userlog.setCreated((int) (System.currentTimeMillis() / 1000));
                userlogService.insert(userlog);
                // 收藏奖励
                taskUtils.reward("mark", user, redisTemplate, usersService, taskService);
            }
            service.update(article);
            return Result.getResultJson(200, userlogList.size() > 0 ? "已取消收藏" : "收藏成功", null);

        } catch (Exception e) {
            e.printStackTrace();
            return Result.getResultJson(400, "接口异常", null);
        }
    }

    @GetMapping(value = "/markList")
    @ResponseBody
    public String markList(@RequestParam(value = "page", required = false, defaultValue = "1") Integer page,
                           @RequestParam(value = "limit", required = false, defaultValue = "10") Integer limit,
                           @RequestParam(value = "order", required = false, defaultValue = "created desc") String order,
                           HttpServletRequest request) {
        try {
            String token = request.getHeader("Authorization");
            Users user = getUser(token);
            if (user.getUid() == null || user.toString().isEmpty())
                return Result.getResultJson(201, "用户不存在", null);
            // 查询出收藏列表
            Userlog userlog = new Userlog();
            userlog.setType("articleMark");
            userlog.setUid(user.getUid());
            PageList<Userlog> userlogPageList = userlogService.selectPage(userlog, page, limit);
            List<Userlog> userlogList = userlogPageList.getList();
            JSONArray dataList = new JSONArray();
            for (Userlog _userlog : userlogList) {
                Article article = articleService.selectByKey(_userlog.getCid());
                if (article == null || article.getCid() == null) continue;
                Map<String, Object> data = JSONObject.parseObject(JSONObject.toJSONString(article), Map.class) != null ? JSONObject.parseObject(JSONObject.toJSONString(article), Map.class) : new HashMap<>();
                if (article == null || article.toString().isEmpty()) {
                    data.put("title", "文章已删除");
                    data.put("authorId", 0);
                    data.put("id", _userlog.getCid());
                }
                // 格式化文章信息
                JSONObject opt = new JSONObject();
                List images = new ArrayList<>();

                opt = article.getOpt() != null && !article.getOpt().toString().isEmpty() ? JSONObject.parseObject(article.getOpt()) : new JSONObject();
                if (article.getImages() != null && !article.getImages().toString().isEmpty()) {
                    images = JSONArray.parseArray(article.getImages());
                } else {
                    images = baseFull.getImageSrc(article.getText());
                }
                data.put("opt", opt);

                // 查询作者
                Users articleUser = usersService.selectByKey(article.getAuthorId());
                Map<String, Object> dataArticleUser = JSONObject.parseObject(JSONObject.toJSONString(articleUser));

                // 格式化作者json数据
                JSONArray head_picture = new JSONArray();
                opt = articleUser.getOpt() != null && !articleUser.getOpt().toString().isEmpty() ? JSONObject.parseObject(articleUser.getOpt()) : new JSONObject();
                dataArticleUser.put("opt", opt);
                dataArticleUser.remove("head_picture");
                dataArticleUser.remove("address");
                dataArticleUser.remove("password");
                data.put("authorInfo", dataArticleUser);
                data.put("images", images);
                data.put("text", baseFull.toStrByChinese(article.getText()));
                data.remove("password");

                dataList.add(data);
            }
            Map<String, Object> data = new HashMap<>();
            data.put("page", page);
            data.put("limit", limit);
            data.put("data", dataList);
            data.put("count", dataList.size());
            data.put("total", userlogService.total(userlog));

            return Result.getResultJson(200, "获取成功", data);
        } catch (Exception e) {
            e.printStackTrace();
            return Result.getResultJson(400, "接口异常", null);
        }

    }

    @PostMapping(value = "/deleteUserArticle")
    @ResponseBody
    public String deleteUserArticle(@RequestParam(value = "id") Integer id,
                                    HttpServletRequest request) {
        try {
            String token = request.getHeader("Authorization");
            Users user = getUser(token);
            if (!permission(user)) return Result.getResultJson(201, "无权限", null);

            service.deleteByAuthor(id);
            return Result.getResultJson(200, "已全部清除", null);

        } catch (Exception e) {
            e.printStackTrace();
            return Result.getResultJson(400, "接口异常", null);
        }
    }

    /***
     * 权限判断
     * @param user
     * @return
     */
    private boolean permission(Users user) {
        if (user.getUid() == null || user.toString().isEmpty()) return false;
        if (user.getGroup().equals("administrator") || user.getGroup().equals("editor")) return true;
        return false;
    }

    /***
     * 获取用户信息
     * @param token
     * @return
     */
    private Users getUser(String token) {
        if (token == null || token.isEmpty()) return new Users();
        // 获取用户信息
        DecodedJWT verify = JWT.verify(token);
        return usersService.selectByKey(Integer.parseInt(verify.getClaim("aud").asString()));
    }

    private Article getArticle(int id) {
        return service.selectByKey(id);
    }

    private Boolean hasLike(Users user, Article article) {
        if (user.getUid() == null) return false;
        Userlog userlog = new Userlog();
        // 是否点赞或者是否收藏
        userlog.setType("articleLike");
        userlog.setCid(article.getCid());
        userlog.setUid(user.getUid());
        List<Userlog> userlogList = userlogService.selectList(userlog);
        if (!userlogList.isEmpty()) return true;
        return false;
    }

    private Boolean hasMark(Users user, Article article) {
        if (user.getUid() == null) return false;
        Userlog userlog = new Userlog();
        userlog.setType("articleMark");
        userlog.setUid(user.getUid());
        userlog.setCid(article.getCid());
        List<Userlog> userlogList = userlogService.selectList(userlog);
        if (!userlogList.isEmpty()) return true;
        return false;
    }

    /***
     * 处理隐藏内同
     * @param isPaid
     * @param isReply
     * @param permission
     * @param article
     * @param user_id
     * @return
     */
    private String hideText(Boolean isPaid, Boolean isReply, Boolean permission, Article article, Integer user_id) {
        Pattern pattern = Pattern.compile("\\[hide type=(pay|reply)\\](.*?)\\[/hide\\]");
        Matcher matcher = pattern.matcher(article.getText());
        StringBuffer replacedText = new StringBuffer();
        while (matcher.find()) {
            String type = matcher.group(1);
            String content = matcher.group(2);
            String replacement = "";
            if (type.equals("pay") && !isPaid && !article.getAuthorId().equals(user_id) && !permission) {
                replacement = "【付费查看：这是付费内容，付费后可查看】";
            } else if (type.equals("reply") && !isReply && !article.getAuthorId().equals(user_id) && !permission) {
                replacement = "【回复查看：这是回复内容，回复后可查看】";
            } else {
                replacement = content;  // 如果不需要替换，则保持原样
            }
            matcher.appendReplacement(replacedText, replacement);
        }
        return matcher.appendTail(replacedText).toString();
    }

    /***
     * 是否评论
     * @param user
     * @param article
     * @return
     */
    private Boolean hasComment(Users user, Article article) {
        if (user.getUid() == null) return false;
        // 获取评论状态
        Comments replyStatus = new Comments();
        replyStatus.setCid(article.getCid());
        replyStatus.setUid(user.getUid());
        int rStatus = commentsService.total(replyStatus, null);
        if (rStatus > 0) return true;
        return false;
    }

    /***
     * 是否支付
     * @param user
     * @param article
     * @return
     */
    private Boolean hasPay(Users user, Article article) {
        if (user.getUid() == null) return false;
        // 获取购买状态
        Paylog paylog = new Paylog();
        paylog.setPaytype("article");
        paylog.setUid(user.getUid());
        paylog.setCid(article.getCid());
        int pStatus = paylogService.total(paylog);
        if (pStatus > 0) return true;

        return false;
    }

    /***
     * 获取分类信息
     * @param id
     * @return
     */
    private Map<String, Object> getCategory(Integer id) {
        // 获取分类和tag
        Category category = metasService.selectByKey(id);

        if (category == null) {
            return new HashMap<>();
        }

        Map<String, Object> data = JSONObject.parseObject(JSONObject.toJSONString(category), Map.class);

        if (category.getOpt() != null) {
            data.put("opt", JSONObject.parseObject(category.getOpt()));
        }

        return data;
    }


    /***
     * 判断是否是会员可见
     * @param user
     * @param article
     * @return
     */
    private Boolean showText(Users user, Article article, Map<String, Object> category) {
        if (category == null || category.isEmpty()) {
            return true;
        }

        if (category.containsKey("isvip") && category.get("isvip").equals(0)) {
            return true;
        }

        if (user.getUid() != null && user.getUid() != 0) {
            if (category.containsKey("isvip") && category.get("isvip").equals(1) && (user.getVip() > System.currentTimeMillis() / 1000 || article.getAuthorId().equals(user.getUid()))) {
                return true;
            }
        }

        return false;
    }

    private Map<String, Object> getAuthorInfo(Integer user_id, Article article) {
        Users author = usersService.selectByKey(article.getAuthorId());
        Map<String, Object> data = new HashMap<>();

        if (author == null) {
            // 作者不存在的默认信息
            data.put("screenName", "用户已注销");
            data.put("isFollow", false);
            data.put("level", 0);
            data.put("nextLevel", 0);
            data.put("isVip", false);
            return data;
        }

        // 作者存在的情况下，继续处理作者信息
        data = JSONObject.parseObject(JSONObject.toJSONString(author), Map.class);
        List levelResult = baseFull.getLevel(author.getExperience(), dataprefix, apiconfigService, redisTemplate);
        int level = (int) levelResult.get(0);
        int nextLevel = (int) levelResult.get(1);
        Boolean isFollow = false;
        Boolean isVip = false;

        // 查询是否关注 是否会员
        if (author.getUid() != null) {
            if (author.getVip() > System.currentTimeMillis() / 1000) isVip = true;
            Fan fan = new Fan();
            fan.setUid(user_id);
            fan.setTouid(article.getAuthorId());
            if (fanService.total(fan) > 0) isFollow = true;
        }

        data.put("isFollow", isFollow);
        data.put("level", level);
        data.put("nextLevel", nextLevel);
        if (author.getOpt() != null) {
            data.put("opt", JSONObject.parseObject(author.getOpt()));
        }
        data.put("isVip", isVip);

        // 移除敏感信息
        data.remove("address");
        data.remove("mail");
        data.remove("assets");
        data.remove("password");

        return data;
    }


    /***
     * 格式化视频
     * @param videosString
     * @return
     */
    private List getVideo(String videosString) {
        if (videosString == null || videosString.isEmpty()) return new ArrayList<>();
        List videos = JSONArray.parseArray(videosString);
        return videos;
    }

    /***
     * 获取封面
     * @param videos
     * @return
     */
    private List getPoster(List videos) {
        List postersList = new ArrayList<>();
        for (int i = 0; i < videos.size(); i++) {
            JSONObject videoObject = JSONObject.parseObject(videos.get(i).toString());
            String poster = videoObject.getString("poster");
            postersList.add(poster);
        }
        return postersList;
    }

    private List getTags(Article article) {
        // 标签
        Relationships tagQuery = new Relationships();
        tagQuery.setCid(article.getCid());
        List<Relationships> tagList = relationshipsService.selectList(tagQuery);
        JSONArray tagDataList = new JSONArray();
        for (Relationships tag : tagList) {
            Category tagsQuery = new Category();
            tagsQuery.setMid(tag.getMid());
            tagsQuery.setType("tag");
            List<Category> tagInfo = metasService.selectList(tagsQuery);
            if (!tagInfo.isEmpty()) {
                Map<String, Object> tagData = JSONObject.parseObject(JSONObject.toJSONString(tagInfo.get(0)), Map.class);
                // 格式化opt
                if (tagData.get("opt") != null) {
                    try {
                        JSONObject.parseObject(tagData.get("opt").toString());
                    } catch (Exception ignored) {
                        tagData.put("opt", null);
                    }
                }
                tagDataList.add(tagData);
            }
        }
        return tagDataList;
    }
}